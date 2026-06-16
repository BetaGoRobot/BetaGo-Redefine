package geocode

import (
	"context"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/luckin"
)

func TestAmapParsesLocation(t *testing.T) {
	point, err := parseAmapLocation("116.397428,39.90923")
	if err != nil {
		t.Fatalf("parse error = %v", err)
	}
	if point.Longitude != 116.397428 || point.Latitude != 39.90923 {
		t.Fatalf("point mismatch: %+v", point)
	}
}

func TestAmapProviderRequiresKey(t *testing.T) {
	p := NewAmapProvider("")
	if _, err := p.Geocode(context.Background(), "上海"); err != ErrAmapKeyMissing {
		t.Fatalf("err = %v, want ErrAmapKeyMissing", err)
	}
}

func TestCachedFallsBackAndCaches(t *testing.T) {
	primary := &countingProvider{err: ErrAmapKeyMissing}
	fallback := &countingProvider{point: luckin.GeoPoint{Longitude: 1.1, Latitude: 2.2}}
	cached := NewCached(primary, fallback)

	point, err := cached.Geocode(context.Background(), "上海人民广场")
	if err != nil {
		t.Fatalf("Geocode error = %v", err)
	}
	if point.Longitude != 1.1 {
		t.Fatalf("point mismatch: %+v", point)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d", fallback.calls)
	}

	// 第二次相同 query 命中缓存，不再调用任何 provider。
	if _, err := cached.Geocode(context.Background(), "上海人民广场"); err != nil {
		t.Fatalf("Geocode2 error = %v", err)
	}
	if fallback.calls != 1 {
		t.Fatalf("expected cache hit, fallback calls = %d", fallback.calls)
	}
}

func TestWGS84ToGCJ02ShiftsInsideChina(t *testing.T) {
	lng, lat := wgs84ToGCJ02(116.397428, 39.90923)
	if lng == 116.397428 || lat == 39.90923 {
		t.Fatalf("expected coordinate shift inside china")
	}
	// 偏移量应在合理范围内（百米级 ~ 0.01 度内）。
	if diff := lng - 116.397428; diff < 0 || diff > 0.02 {
		t.Fatalf("unexpected lng shift: %f", diff)
	}
}

func TestWGS84ToGCJ02NoShiftOutsideChina(t *testing.T) {
	lng, lat := wgs84ToGCJ02(2.3522, 48.8566) // Paris
	if lng != 2.3522 || lat != 48.8566 {
		t.Fatalf("expected no shift outside china")
	}
}

type countingProvider struct {
	point luckin.GeoPoint
	err   error
	calls int
}

func (p *countingProvider) Name() string { return "counting" }

func (p *countingProvider) Geocode(ctx context.Context, address string) (luckin.GeoPoint, error) {
	p.calls++
	if p.err != nil {
		return luckin.GeoPoint{}, p.err
	}
	return p.point, nil
}
