package business

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
)

type NominatimGeocoder struct{ http *http.Client }

func NewNominatimGeocoder(c *http.Client) *NominatimGeocoder { return &NominatimGeocoder{http: c} }
func (g *NominatimGeocoder) Locate(ctx context.Context, address string) (GeoPoint, error) {
	q := url.Values{"q": {address}, "format": {"json"}, "limit": {"1"}}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://nominatim.openstreetmap.org/search?"+q.Encode(), nil)
	if err != nil {
		return GeoPoint{}, ErrProvider
	}
	req.Header.Set("User-Agent", "BetaGo-Agent/1.0 (Luckin location lookup)")
	b, err := readResponse(g.http, req, 64<<10)
	if err != nil {
		return GeoPoint{}, err
	}
	var rows []struct{ Lat, Lon string }
	if json.Unmarshal(b, &rows) != nil || len(rows) == 0 {
		return GeoPoint{}, ErrProvider
	}
	lat, e := strconv.ParseFloat(rows[0].Lat, 64)
	lon, e2 := strconv.ParseFloat(rows[0].Lon, 64)
	if e != nil || e2 != nil || math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return GeoPoint{}, ErrProvider
	}
	lon, lat = wgs84ToGCJ02(lon, lat)
	return GeoPoint{Longitude: lon, Latitude: lat}, nil
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
