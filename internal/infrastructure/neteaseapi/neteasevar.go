package neteaseapi

import (
	"context"
	"errors"
	"net/http"
	"os"
)

// IsTest 是否测试环境
var IsTest = os.Getenv("IS_TEST")

// LoginStatusStruct  登录状态
type LoginStatusStruct struct {
	Data struct {
		Code    int                    `json:"code"`
		Account map[string]interface{} `json:"account"`
		Profile map[string]interface{} `json:"profile"`
	} `json:"data"`
}

// Provider 网易云能力抽象。
type Provider interface {
	SearchAlbumByKeyWord(ctx context.Context, keywords ...string) ([]*Album, error)
	SearchMusicByKeyWord(ctx context.Context, keywords ...string) ([]*SearchMusicItem, error)
	SearchMusicByPlaylist(ctx context.Context, playlistID string) (*PlaylistDetail, []*SearchMusicItem, error)
	GetMusicURL(ctx context.Context, id int) (string, error)
	GetDetail(ctx context.Context, musicID int) *MusicDetail
	GetLyrics(ctx context.Context, songID int) (string, string)
	GetAlbumDetail(ctx context.Context, albumID string) (*AlbumDetail, error)
	AsyncGetSearchRes(ctx context.Context, searchRes SearchMusic) ([]*SearchMusicItem, error)
	GetComment(ctx context.Context, commentType CommentType, id string) (*CommentResult, error)
	TryGetLastCookie(ctx context.Context)
	LoginNetEase(ctx context.Context) error
	LoginNetEaseQR(ctx context.Context) error
	CheckIfLogin(ctx context.Context) bool
	RefreshLogin(ctx context.Context) error
	SaveCookie(ctx context.Context)
}

// NetEaseContext 网易云API调用封装
type NetEaseContext struct {
	cookies  []*http.Cookie
	qrStruct struct {
		isOutDated bool
		uniKey     string
		qrBase64   string
	}
	loginType string
}

type noopProvider struct {
	reason string
}

func (n noopProvider) unavailableErr() error {
	if n.reason == "" {
		return errors.New("netease api not initialized")
	}
	return errors.New(n.reason)
}

func (n noopProvider) SearchAlbumByKeyWord(context.Context, ...string) ([]*Album, error) {
	return nil, n.unavailableErr()
}

func (n noopProvider) SearchMusicByKeyWord(context.Context, ...string) ([]*SearchMusicItem, error) {
	return nil, n.unavailableErr()
}

func (n noopProvider) SearchMusicByPlaylist(context.Context, string) (*PlaylistDetail, []*SearchMusicItem, error) {
	return nil, nil, n.unavailableErr()
}

func (n noopProvider) GetMusicURL(context.Context, int) (string, error) {
	return "", n.unavailableErr()
}

func (n noopProvider) GetDetail(context.Context, int) *MusicDetail {
	return &MusicDetail{}
}

func (n noopProvider) GetLyrics(context.Context, int) (string, string) {
	return "", ""
}

func (n noopProvider) GetAlbumDetail(context.Context, string) (*AlbumDetail, error) {
	return nil, n.unavailableErr()
}

func (n noopProvider) AsyncGetSearchRes(context.Context, SearchMusic) ([]*SearchMusicItem, error) {
	return nil, n.unavailableErr()
}

func (n noopProvider) GetComment(context.Context, CommentType, string) (*CommentResult, error) {
	return nil, n.unavailableErr()
}

func (n noopProvider) TryGetLastCookie(context.Context)     {}
func (n noopProvider) LoginNetEase(context.Context) error   { return n.unavailableErr() }
func (n noopProvider) LoginNetEaseQR(context.Context) error { return n.unavailableErr() }
func (n noopProvider) CheckIfLogin(context.Context) bool    { return false }
func (n noopProvider) RefreshLogin(context.Context) error   { return n.unavailableErr() }
func (n noopProvider) SaveCookie(context.Context)           {}

type dailySongs struct {
	Data struct {
		DailySongs []struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"dailySongs"`
	} `json:"data"`
}
type musicList struct {
	Data []*musicData `json:"data"`
}

type musicData struct {
	ID  int    `json:"id"`
	URL string `json:"url"`
}

type SearchMusic struct {
	Result struct {
		Songs []Song `json:"songs"`
	} `json:"result"`
}

type searchAlbumResult struct {
	Result struct {
		Albums []*Album `json:"albums"`
	} `json:"result"`
}

type AlbumDetail struct {
	Songs []Song `json:"songs"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
}

type Album struct {
	Name        string `json:"name"`
	ID          int64  `json:"id"`
	IDStr       string `json:"idStr"`
	Type        string `json:"type"`
	PicURL      string `json:"picUrl"`
	PublishTime int64  `json:"publishTime"`
	Artist      struct {
		Name string `json:"name"`
	} `json:"artist"`
}
type Playlist struct {
	Result struct {
		SearchQcReminder interface{} `json:"searchQcReminder"`
		Playlists        []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			CoverImgURL string `json:"coverImgUrl"`
			Creator     struct {
				Nickname   string      `json:"nickname"`
				UserID     int         `json:"userId"`
				UserType   int         `json:"userType"`
				AvatarURL  interface{} `json:"avatarUrl"`
				AuthStatus int         `json:"authStatus"`
				ExpertTags interface{} `json:"expertTags"`
				Experts    interface{} `json:"experts"`
			} `json:"creator"`
			Subscribed    bool        `json:"subscribed"`
			TrackCount    int         `json:"trackCount"`
			UserID        int         `json:"userId"`
			PlayCount     int         `json:"playCount"`
			BookCount     int         `json:"bookCount"`
			SpecialType   int         `json:"specialType"`
			OfficialTags  interface{} `json:"officialTags"`
			Action        interface{} `json:"action"`
			ActionType    interface{} `json:"actionType"`
			RecommendText interface{} `json:"recommendText"`
			Score         interface{} `json:"score"`
			Description   string      `json:"description"`
			HighQuality   bool        `json:"highQuality"`
		} `json:"playlists"`
		PlaylistCount int `json:"playlistCount"`
	} `json:"result"`
	Code int `json:"code"`
}

type PlaylistDetailResponse struct {
	Playlist PlaylistDetail `json:"playlist"`
	Code     int            `json:"code"`
}

type PlaylistDetail struct {
	ID       int64                   `json:"id"`
	Name     string                  `json:"name"`
	TrackIDs []PlaylistTrackIdentity `json:"trackIds"`
	Tracks   []MusicDetailSongLite   `json:"tracks"`
}

type PlaylistTrackIdentity struct {
	ID int `json:"id"`
	V  int `json:"v"`
	T  int `json:"t"`
}

type MusicDetailSongLite struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Song struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
	Ar   []struct {
		Name string `json:"name"`
	} `json:"ar"`
	Al struct {
		PicURL string `json:"picUrl"`
	} `json:"al"`
}

// SearchMusicItem  搜索音乐返回结果
type SearchMusicItem struct {
	Index      int
	ID         int
	Name       string
	ArtistName string
	SongURL    string
	PicURL     string
	ImageKey   string
}

type CommentResult struct {
	Data struct {
		CommentsTitle string `json:"commentsTitle"`
		Comments      []struct {
			Content string `json:"content"`
			TimeStr string `json:"timeStr"`
		}
	} `json:"data"`
	Message string `json:"message"`
}

// GlobRecommendMusicRes  推荐音乐返回结果
type GlobRecommendMusicRes struct {
	Result []struct {
		PicURL string `json:"picUrl"`
		Song   struct {
			Name    string `json:"name"`
			ID      int    `json:"id"`
			Artists []struct {
				Name string `json:"name"`
				ID   int    `json:"id"`
			} `json:"artists"`
		} `json:"song"`
	} `json:"result"`
}

type SearchLyrics struct {
	Sgc       bool `json:"sgc"`
	Sfy       bool `json:"sfy"`
	Qfy       bool `json:"qfy"`
	TransUser struct {
		ID       int    `json:"id"`
		Status   int    `json:"status"`
		Demand   int    `json:"demand"`
		Userid   int    `json:"userid"`
		Nickname string `json:"nickname"`
		Uptime   int64  `json:"uptime"`
	} `json:"transUser"`
	LyricUser struct {
		ID       int    `json:"id"`
		Status   int    `json:"status"`
		Demand   int    `json:"demand"`
		Userid   int    `json:"userid"`
		Nickname string `json:"nickname"`
		Uptime   int64  `json:"uptime"`
	} `json:"lyricUser"`
	Lrc struct {
		Version int    `json:"version"`
		Lyric   string `json:"lyric"`
	} `json:"lrc"`
	Klyric struct {
		Version int    `json:"version"`
		Lyric   string `json:"lyric"`
	} `json:"klyric"`
	Tlyric struct {
		Version int    `json:"version"`
		Lyric   string `json:"lyric"`
	} `json:"tlyric"`
	Romalrc struct {
		Version int    `json:"version"`
		Lyric   string `json:"lyric"`
	} `json:"romalrc"`
	Code int `json:"code"`
}

type MusicDetail struct {
	Songs      []*MusicDetailSong `json:"songs"`
	Privileges []struct {
		ID                 int         `json:"id"`
		Fee                int         `json:"fee"`
		Payed              int         `json:"payed"`
		St                 int         `json:"st"`
		Pl                 int         `json:"pl"`
		Dl                 int         `json:"dl"`
		Sp                 int         `json:"sp"`
		Cp                 int         `json:"cp"`
		Subp               int         `json:"subp"`
		Cs                 bool        `json:"cs"`
		Maxbr              int         `json:"maxbr"`
		Fl                 int         `json:"fl"`
		Toast              bool        `json:"toast"`
		Flag               int         `json:"flag"`
		PreSell            bool        `json:"preSell"`
		PlayMaxbr          int         `json:"playMaxbr"`
		DownloadMaxbr      int         `json:"downloadMaxbr"`
		MaxBrLevel         string      `json:"maxBrLevel"`
		PlayMaxBrLevel     string      `json:"playMaxBrLevel"`
		DownloadMaxBrLevel string      `json:"downloadMaxBrLevel"`
		PlLevel            string      `json:"plLevel"`
		DlLevel            string      `json:"dlLevel"`
		FlLevel            string      `json:"flLevel"`
		Rscl               interface{} `json:"rscl"`
		FreeTrialPrivilege struct {
			ResConsumable      bool        `json:"resConsumable"`
			UserConsumable     bool        `json:"userConsumable"`
			ListenType         interface{} `json:"listenType"`
			CannotListenReason interface{} `json:"cannotListenReason"`
			PlayReason         interface{} `json:"playReason"`
		} `json:"freeTrialPrivilege"`
		RightSource    int `json:"rightSource"`
		ChargeInfoList []struct {
			Rate          int         `json:"rate"`
			ChargeURL     interface{} `json:"chargeUrl"`
			ChargeMessage interface{} `json:"chargeMessage"`
			ChargeType    int         `json:"chargeType"`
		} `json:"chargeInfoList"`
	} `json:"privileges"`
	Code int `json:"code"`
}

type MusicDetailSong struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
	Pst  int    `json:"pst"`
	T    int    `json:"t"`
	Ar   []struct {
		ID    int           `json:"id"`
		Name  string        `json:"name"`
		Tns   []interface{} `json:"tns"`
		Alias []interface{} `json:"alias"`
	} `json:"ar"`
	Alia []interface{} `json:"alia"`
	Pop  int           `json:"pop"`
	St   int           `json:"st"`
	Rt   string        `json:"rt"`
	Fee  int           `json:"fee"`
	V    int           `json:"v"`
	Crbt interface{}   `json:"crbt"`
	Cf   string        `json:"cf"`
	Al   struct {
		ID     int           `json:"id"`
		Name   string        `json:"name"`
		PicURL string        `json:"picUrl"`
		Tns    []interface{} `json:"tns"`
		PicStr string        `json:"pic_str"`
		Pic    int64         `json:"pic"`
	} `json:"al"`
	Dt int `json:"dt"`
	H  struct {
		Br   int `json:"br"`
		Fid  int `json:"fid"`
		Size int `json:"size"`
		Vd   int `json:"vd"`
		Sr   int `json:"sr"`
	} `json:"h"`
	M struct {
		Br   int `json:"br"`
		Fid  int `json:"fid"`
		Size int `json:"size"`
		Vd   int `json:"vd"`
		Sr   int `json:"sr"`
	} `json:"m"`
	L struct {
		Br   int `json:"br"`
		Fid  int `json:"fid"`
		Size int `json:"size"`
		Vd   int `json:"vd"`
		Sr   int `json:"sr"`
	} `json:"l"`
	Sq                   interface{}   `json:"sq"`
	Hr                   interface{}   `json:"hr"`
	A                    interface{}   `json:"a"`
	Cd                   string        `json:"cd"`
	No                   int           `json:"no"`
	RtURL                interface{}   `json:"rtUrl"`
	Ftype                int           `json:"ftype"`
	RtUrls               []interface{} `json:"rtUrls"`
	DjID                 int           `json:"djId"`
	Copyright            int           `json:"copyright"`
	SID                  int           `json:"s_id"`
	Mark                 int           `json:"mark"`
	OriginCoverType      int           `json:"originCoverType"`
	OriginSongSimpleData interface{}   `json:"originSongSimpleData"`
	TagPicList           interface{}   `json:"tagPicList"`
	ResourceState        bool          `json:"resourceState"`
	Version              int           `json:"version"`
	SongJumpInfo         interface{}   `json:"songJumpInfo"`
	EntertainmentTags    interface{}   `json:"entertainmentTags"`
	AwardTags            interface{}   `json:"awardTags"`
	Single               int           `json:"single"`
	NoCopyrightRcmd      interface{}   `json:"noCopyrightRcmd"`
	Mv                   int           `json:"mv"`
	Rtype                int           `json:"rtype"`
	Rurl                 interface{}   `json:"rurl"`
	Mst                  int           `json:"mst"`
	Cp                   int           `json:"cp"`
	PublishTime          int           `json:"publishTime"`
}

// NetEaseAPIBaseURL 网易云API基础URL
var NetEaseAPIBaseURL = "http://netease-api:3335"

// NetEaseGCtx 网易云全局API调用封装
var NetEaseGCtx Provider = noopProvider{reason: "netease api not initialized"}
