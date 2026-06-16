package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
)

const nominatimURL = "https://nominatim.openstreetmap.org/search"

var (
	errEmptyAddress = errors.New("empty address")
	ErrNoProvider   = errors.New("no geocode provider available")
)

// NominatimProvider 使用 OpenStreetMap 免费地理编码作为兜底，返回 WGS-84，内部转成 GCJ-02。
type NominatimProvider struct {
	http      *http.Client
	userAgent string
}

func NewNominatimProvider() *NominatimProvider {
	return &NominatimProvider{
		http:      &http.Client{Timeout: httpTimeout},
		userAgent: "BetaGo-Redefine/1.0 (luckin geocode fallback)",
	}
}

func (p *NominatimProvider) Name() string { return "nominatim" }

func (p *NominatimProvider) Geocode(ctx context.Context, address string) (luckin.GeoPoint, error) {
	q := url.Values{}
	q.Set("q", address)
	q.Set("format", "json")
	q.Set("limit", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nominatimURL+"?"+q.Encode(), nil)
	if err != nil {
		return luckin.GeoPoint{}, err
	}
	// Nominatim 使用政策要求带 User-Agent。
	req.Header.Set("User-Agent", p.userAgent)

	resp, err := p.http.Do(req)
	if err != nil {
		return luckin.GeoPoint{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return luckin.GeoPoint{}, fmt.Errorf("nominatim status %d", resp.StatusCode)
	}

	var body []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return luckin.GeoPoint{}, err
	}
	if len(body) == 0 {
		return luckin.GeoPoint{}, fmt.Errorf("nominatim no result for %q", address)
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(body[0].Lat), 64)
	if err != nil {
		return luckin.GeoPoint{}, fmt.Errorf("invalid latitude %q", body[0].Lat)
	}
	lng, err := strconv.ParseFloat(strings.TrimSpace(body[0].Lon), 64)
	if err != nil {
		return luckin.GeoPoint{}, fmt.Errorf("invalid longitude %q", body[0].Lon)
	}
	gcjLng, gcjLat := wgs84ToGCJ02(lng, lat)
	return luckin.GeoPoint{Longitude: gcjLng, Latitude: gcjLat}, nil
}

// wgs84ToGCJ02 把 WGS-84 坐标转换为 GCJ-02（国测局火星坐标），与瑞幸/高德坐标系对齐。
func wgs84ToGCJ02(lng, lat float64) (float64, float64) {
	if outOfChina(lng, lat) {
		return lng, lat
	}
	dLat := transformLat(lng-105.0, lat-35.0)
	dLng := transformLng(lng-105.0, lat-35.0)
	radLat := lat / 180.0 * math.Pi
	magic := math.Sin(radLat)
	magic = 1 - eeConst*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((aConst * (1 - eeConst)) / (magic * sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / (aConst / sqrtMagic * math.Cos(radLat) * math.Pi)
	return lng + dLng, lat + dLat
}

const (
	aConst  = 6378245.0
	eeConst = 0.00669342162296594323
)

func outOfChina(lng, lat float64) bool {
	return lng < 72.004 || lng > 137.8347 || lat < 0.8293 || lat > 55.8271
}

func transformLat(x, y float64) float64 {
	ret := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	ret += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return ret
}

func transformLng(x, y float64) float64 {
	ret := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	ret += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return ret
}
