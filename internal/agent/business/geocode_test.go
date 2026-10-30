package business

import (
	"math"
	"testing"
)

func TestGCJ02ConversionPreservesOutsideChinaAndOffsetsShanghai(t *testing.T) {
	x, y := wgs84ToGCJ02(2, 48)
	if x != 2 || y != 48 {
		t.Fatal("outside China changed")
	}
	x, y = wgs84ToGCJ02(121.4737, 31.2304)
	if math.Abs(x-121.47822) > 0.00002 || math.Abs(y-31.22846) > 0.00002 {
		t.Fatalf("wrong GCJ-02 coordinates %.7f %.7f", x, y)
	}
}
