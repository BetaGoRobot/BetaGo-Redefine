package music

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

type SearchHistoryItem struct {
	Query          string    `json:"query"`
	Source         string    `json:"source"`
	Count          int64     `json:"count"`
	LastSearchedAt time.Time `json:"lastSearchedAt"`
}
type RecommendedTrack struct {
	Track  Track  `json:"track"`
	Reason string `json:"reason"`
	Basis  string `json:"basis"`
}
type Recommendations struct {
	Tracks       []RecommendedTrack `json:"tracks"`
	Personalized bool               `json:"personalized"`
}

func requestLimit(r *http.Request, fallback, maximum int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maximum {
		return 0, invalid("invalid_limit")
	}
	return n, nil
}
func normalizedSearch(value string) string {
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
}
func (s *Server) searchHistory(r *http.Request, a account) (any, error) {
	db := s.db.WithContext(r.Context())
	switch r.Method {
	case http.MethodGet:
		limit, err := requestLimit(r, 20, 100)
		if err != nil {
			return nil, err
		}
		rows := []SearchHistoryItem{}
		err = db.Raw("SELECT query,source,count,last_searched_at FROM music_search_history WHERE owner_id=? ORDER BY last_searched_at DESC,source,query_key LIMIT ?", a.ID, limit).Scan(&rows).Error
		return map[string]any{"searches": rows}, err
	case http.MethodPost:
		var in struct {
			Query  string `json:"query"`
			Source string `json:"source"`
		}
		if err := body(r, &in); err != nil {
			return nil, err
		}
		query := normalizedSearch(in.Query)
		if query == "" || len([]rune(query)) > 200 || len(query) > 800 {
			return nil, invalid("invalid_query")
		}
		if in.Source == "" {
			in.Source = "netease"
		}
		if in.Source != "netease" && in.Source != "kuwo" {
			return nil, invalid("source_not_configured")
		}
		if choices, ok := s.opts.Provider.(catalogueOptionsProvider); ok {
			configured := false
			for _, source := range choices.CatalogueOptions().Sources {
				configured = configured || source.ID == in.Source
			}
			if !configured {
				return nil, invalid("source_not_configured")
			}
		} else if in.Source != "netease" {
			return nil, invalid("source_not_configured")
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			// Bound concurrent history upserts and pruning for this owner only.
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "music-search:"+a.ID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO music_search_history(owner_id,source,query_key,query,count,last_searched_at) VALUES(?,?,?,?,1,?) ON CONFLICT(owner_id,source,query_key) DO UPDATE SET query=EXCLUDED.query,count=LEAST(music_search_history.count+1,1000000),last_searched_at=EXCLUDED.last_searched_at`, a.ID, in.Source, digest(strings.ToLower(query)), query, s.opts.Now().UTC()).Error; err != nil {
				return err
			}
			return tx.Exec(`DELETE FROM music_search_history WHERE owner_id=? AND (source,query_key) NOT IN (SELECT source,query_key FROM music_search_history WHERE owner_id=? ORDER BY last_searched_at DESC,source,query_key LIMIT 100)`, a.ID, a.ID).Error
		})
		return map[string]bool{"ok": true}, err
	case http.MethodDelete:
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "music-search:"+a.ID).Error; err != nil {
				return err
			}
			return tx.Exec("DELETE FROM music_search_history WHERE owner_id=?", a.ID).Error
		})
		return map[string]bool{"ok": true}, err
	default:
		return nil, missing
	}
}

type interestArtist struct {
	Name, Basis string
	Weight      int
}

func primaryArtist(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == '/' || r == '&' || r == '、' })
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}
func normalizedInterest(value string) string { return strings.ToLower(normalizedSearch(value)) }

// RecommendForIdentity is only for trusted, already-verified platform events.
// Public HTTP recommendations use the authenticated music session instead.
func RecommendForIdentity(ctx context.Context, db *gorm.DB, provider Provider, identity Identity, limit int) (Recommendations, error) {
	if db == nil || identity.AppID == "" || identity.TenantKey == "" || identity.Subject == "" {
		return Recommendations{}, missing
	}
	var user account
	result := uncached(db).WithContext(ctx).Raw("SELECT id,kind FROM music_users WHERE kind='feishu' AND app_id=? AND tenant_key=? AND subject=?", identity.AppID, identity.TenantKey, identity.Subject).Scan(&user)
	if result.Error != nil {
		return Recommendations{}, result.Error
	}
	if result.RowsAffected != 1 {
		return Recommendations{}, missing
	}
	return recommendOwned(ctx, uncached(db), provider, user.ID, limit)
}

func recommendOwned(ctx context.Context, db *gorm.DB, provider Provider, owner string, limit int) (Recommendations, error) {
	out := Recommendations{Tracks: []RecommendedTrack{}}
	if limit < 1 || limit > 50 {
		return out, invalid("invalid_limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	db = db.WithContext(ctx)
	liked := []Track{}
	history := []Track{}
	queries := []SearchHistoryItem{}
	if err := db.Raw("SELECT t.* FROM music_likes l JOIN music_tracks t ON t.id=l.track_id WHERE l.owner_id=? AND t.canonical_verified=true ORDER BY t.id LIMIT 100", owner).Scan(&liked).Error; err != nil {
		return out, err
	}
	if err := db.Raw("SELECT t.* FROM music_history h JOIN music_tracks t ON t.id=h.track_id WHERE h.owner_id=? AND t.canonical_verified=true ORDER BY h.played_at DESC,t.id LIMIT 50", owner).Scan(&history).Error; err != nil {
		return out, err
	}
	if err := db.Raw("SELECT query,source,count,last_searched_at FROM music_search_history WHERE owner_id=? ORDER BY last_searched_at DESC LIMIT 5", owner).Scan(&queries).Error; err != nil {
		return out, err
	}
	excluded := map[string]bool{}
	artists := map[string]interestArtist{}
	addInterests := func(tracks []Track, basis string, weight int) {
		for _, t := range tracks {
			excluded[t.ID] = true
			name := primaryArtist(t.Artist)
			key := normalizedInterest(name)
			if key == "" {
				continue
			}
			item := artists[key]
			item.Name = name
			item.Weight += weight
			if item.Basis == "" || basis == "likes" {
				item.Basis = basis
			}
			artists[key] = item
		}
	}
	addInterests(liked, "likes", 5)
	addInterests(history, "history", 2)
	candidates := map[string]Track{}
	known := []Track{}
	if err := db.Raw("SELECT * FROM music_tracks WHERE canonical_verified=true ORDER BY id LIMIT 500").Scan(&known).Error; err != nil {
		return out, err
	}
	for _, t := range known {
		candidates[t.ID] = t
	}
	interestList := make([]interestArtist, 0, len(artists))
	for _, v := range artists {
		interestList = append(interestList, v)
	}
	sort.Slice(interestList, func(i, j int) bool {
		if interestList[i].Weight == interestList[j].Weight {
			return interestList[i].Name < interestList[j].Name
		}
		return interestList[i].Weight > interestList[j].Weight
	})
	searches := []string{}
	if len(interestList) > 0 {
		searches = append(searches, interestList[0].Name)
	}
	if len(queries) > 0 {
		searches = append(searches, queries[0].Query)
	} else if len(interestList) > 1 {
		searches = append(searches, interestList[1].Name)
	}
	if len(searches) == 0 && len(known) < limit {
		searches = []string{"流行"}
	}
	// Search metadata only. No resolve, account source, playback URL or feedback
	// insertion occurs while generating recommendations.
	if provider != nil {
		for _, query := range searches {
			if ctx.Err() != nil {
				break
			}
			query = normalizedSearch(query)
			if len(query) > 200 || query == "" {
				continue
			}
			searchCtx, stop := context.WithTimeout(ctx, 2500*time.Millisecond)
			tracks, err := provider.Search(searchCtx, query)
			stop()
			if err != nil {
				continue
			}
			if len(tracks) > 100 {
				tracks = tracks[:100]
			}
			for _, t := range tracks {
				if err := saveTrack(db, t); err == nil {
					candidates[t.ID] = t
				}
			}
		}
	}
	type ranked struct {
		Item  RecommendedTrack
		Score int
	}
	rankedTracks := []ranked{}
	for id, t := range candidates {
		if excluded[id] || validateID(id) != nil {
			continue
		}
		score := 0
		basis := "discovery"
		reason := "曲库发现，试试不同的声音"
		if artist, ok := artists[normalizedInterest(primaryArtist(t.Artist))]; ok {
			score = 50 + artist.Weight
			basis = artist.Basis
			if basis == "likes" {
				reason = "根据你喜欢的歌手：" + artist.Name
			} else {
				reason = "根据你最近听的歌手：" + artist.Name
			}
		}
		if score == 0 {
			for _, q := range queries {
				term := normalizedInterest(q.Query)
				if term != "" && strings.Contains(normalizedInterest(t.Title+" "+t.Artist+" "+t.Album), term) {
					score = 30
					basis = "search"
					reason = "根据你最近的搜索：" + q.Query
					break
				}
			}
		}
		rankedTracks = append(rankedTracks, ranked{RecommendedTrack{t, reason, basis}, score})
	}
	sort.Slice(rankedTracks, func(i, j int) bool {
		if rankedTracks[i].Score == rankedTracks[j].Score {
			return rankedTracks[i].Item.Track.ID < rankedTracks[j].Item.Track.ID
		}
		return rankedTracks[i].Score > rankedTracks[j].Score
	})
	for _, candidate := range rankedTracks {
		out.Tracks = append(out.Tracks, candidate.Item)
		out.Personalized = out.Personalized || candidate.Item.Basis != "discovery"
		if len(out.Tracks) == limit {
			break
		}
	}
	return out, nil
}
