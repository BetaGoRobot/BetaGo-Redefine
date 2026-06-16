package geocode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
)

const (
	amapGeoURL  = "https://restapi.amap.com/v3/geocode/geo"
	httpTimeout = 8 * time.Second
)

var ErrAmapKeyMissing = errors.New("amap key is not configured")

// AmapProvider 调用高德地理编码接口，返回 GCJ-02 经纬度，与瑞幸坐标系一致（最准）。
type AmapProvider struct {
	key  string
	http *http.Client
}

func NewAmapProvider(key string) *AmapProvider {
	return &AmapProvider{
		key:  strings.TrimSpace(key),
		http: &http.Client{Timeout: httpTimeout},
	}
}

func (p *AmapProvider) Name() string { return "amap" }

func (p *AmapProvider) Geocode(ctx context.Context, address string) (luckin.GeoPoint, error) {
	if p.key == "" {
		return luckin.GeoPoint{}, ErrAmapKeyMissing
	}
	q := url.Values{}
	q.Set("key", p.key)
	q.Set("address", address)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, amapGeoURL+"?"+q.Encode(), nil)
	if err != nil {
		return luckin.GeoPoint{}, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return luckin.GeoPoint{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return luckin.GeoPoint{}, fmt.Errorf("amap geocode status %d", resp.StatusCode)
	}

	var body struct {
		Status   string `json:"status"`
		Info     string `json:"info"`
		Geocodes []struct {
			Location string `json:"location"`
		} `json:"geocodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return luckin.GeoPoint{}, err
	}
	if body.Status != "1" {
		return luckin.GeoPoint{}, fmt.Errorf("amap geocode failed: %s", body.Info)
	}
	if len(body.Geocodes) == 0 {
		return luckin.GeoPoint{}, fmt.Errorf("amap geocode no result for %q", address)
	}
	return parseAmapLocation(body.Geocodes[0].Location)
}

func parseAmapLocation(location string) (luckin.GeoPoint, error) {
	parts := strings.Split(strings.TrimSpace(location), ",")
	if len(parts) != 2 {
		return luckin.GeoPoint{}, fmt.Errorf("invalid amap location %q", location)
	}
	lng, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return luckin.GeoPoint{}, fmt.Errorf("invalid longitude %q", parts[0])
	}
	lat, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return luckin.GeoPoint{}, fmt.Errorf("invalid latitude %q", parts[1])
	}
	return luckin.GeoPoint{Longitude: lng, Latitude: lat}, nil
}
