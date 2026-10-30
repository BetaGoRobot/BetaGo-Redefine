package musicmedia

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

const assetPrefix = "/music/assets/"

type assetClaim struct {
	Purpose string `json:"p"`
	Key     string `json:"k"`
	Expires int64  `json:"e"`
}

func (s *Service) assetURL(key string, expiry time.Time) string {
	b, _ := json.Marshal(assetClaim{Purpose: "music-public-v1", Key: key, Expires: expiry.Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	h := hmac.New(sha256.New, s.cfg.SigningKey)
	h.Write([]byte("betago-music-assets-v1\x00" + payload))
	return s.cfg.PublicURL + assetPrefix + payload + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Service) claim(path string) (assetClaim, bool) {
	var claim assetClaim
	if !strings.HasPrefix(path, assetPrefix) || len(path) > 4096 {
		return claim, false
	}
	token := strings.TrimPrefix(path, assetPrefix)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return claim, false
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return claim, false
	}
	h := hmac.New(sha256.New, s.cfg.SigningKey)
	h.Write([]byte("betago-music-assets-v1\x00" + parts[0]))
	if !hmac.Equal(signature, h.Sum(nil)) {
		return claim, false
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(b, &claim) != nil {
		return claim, false
	}
	if claim.Purpose != "music-public-v1" || claim.Expires <= time.Now().Unix() || !strings.HasPrefix(claim.Key, "music/v1/") {
		return claim, false
	}
	return claim, true
}

// Handler serves only purpose-signed published music objects. It grants neither
// conversation access nor arbitrary object-store access. Mount at /music/assets/.
func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serveAsset) }
func (s *Service) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	claim, ok := s.claim(r.URL.Path)
	if !ok {
		http.Error(w, "invalid music asset link", http.StatusForbidden)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Add("Vary", "Origin")
		if origin == s.playerOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Range")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges, ETag, Last-Modified")
		}
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ObjectStore.Timeout)
	defer cancel()
	var size int64
	var mime, digest, scope, purpose string
	var created time.Time
	e := s.cfg.DB.QueryRowContext(ctx, `SELECT scope_key,purpose,mime,size,digest,created_at FROM `+s.table("objects")+` WHERE object_key=$1`, claim.Key).Scan(&scope, &purpose, &mime, &size, &digest, &created)
	if e != nil || size <= 0 || size > s.cfg.ObjectStore.MaxBytes {
		http.Error(w, "music asset unavailable", http.StatusNotFound)
		return
	}
	obj, e := s.objects.GetObject(ctx, s.cfg.ObjectStore.Bucket, claim.Key, minio.GetObjectOptions{})
	if e != nil {
		http.Error(w, "music asset unavailable", http.StatusBadGateway)
		return
	}
	defer obj.Close()
	info, e := obj.Stat()
	if e != nil || info.Size != size || info.UserMetadata["Sha256"] != digest || info.UserMetadata["Scope"] != scope || info.UserMetadata["Purpose"] != purpose {
		http.Error(w, "music asset unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+digest+`"`)
	remaining := claim.Expires - time.Now().Unix()
	if remaining < 0 {
		http.Error(w, "expired music asset link", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age="+strconv.FormatInt(min(remaining, 3600), 10))
	http.ServeContent(w, r, "music-"+purpose, created, obj)
}
