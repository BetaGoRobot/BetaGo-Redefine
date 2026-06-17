package luckin

import (
	"strings"
	"sync"

	"github.com/issue9/cnregion"
	"github.com/issue9/cnregion/data"
	"github.com/issue9/cnregion/id"
)

var (
	regionDataOnce sync.Once
	regionData     *cnregion.Version
)

func RegionOptions(limit int) []string {
	regions := loadRegionData()
	if regions == nil || limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for _, province := range regions.Provinces() {
		for _, city := range province.Items() {
			if appendRegionName(&out, city, limit) {
				return out
			}
			for _, county := range city.Items() {
				if appendRegionName(&out, county, limit) {
					return out
				}
			}
		}
	}
	return out
}

func ProvinceOptions(limit int) []string {
	regions := loadRegionData()
	if regions == nil || limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for _, province := range regions.Provinces() {
		if appendRegionName(&out, province, limit) {
			return out
		}
	}
	return out
}

func CityCountyOptions(provinceName string, limit int) []string {
	provinceName = strings.TrimSpace(provinceName)
	regions := loadRegionData()
	if regions == nil || provinceName == "" || limit <= 0 {
		return nil
	}
	for _, province := range regions.Provinces() {
		if !regionNameMatches(provinceName, province) {
			continue
		}
		out := make([]string, 0, limit)
		for _, city := range province.Items() {
			if appendRegionName(&out, city, limit) {
				return out
			}
			for _, county := range city.Items() {
				if appendRegionName(&out, county, limit) {
					return out
				}
			}
		}
		return out
	}
	return nil
}

func appendRegionName(out *[]string, region cnregion.Region, limit int) bool {
	if region == nil || len(*out) >= limit {
		return len(*out) >= limit
	}
	name := compactRegionName(region)
	if name == "" {
		return false
	}
	*out = append(*out, name)
	return len(*out) >= limit
}

func NormalizeLocationText(location string) string {
	location = strings.TrimSpace(location)
	if location == "" {
		return ""
	}
	region := matchRegion(location)
	if region == nil {
		return location
	}
	name := compactRegionName(region)
	if name == "" || strings.Contains(location, name) {
		return location
	}
	location = trimRegionSuffix(location, region.Name())
	return strings.TrimSpace(name + " " + location)
}

func matchRegion(location string) cnregion.Region {
	regions := loadRegionData()
	if regions == nil {
		return nil
	}
	candidates := []cnregion.Region{}
	parts := strings.Fields(location)
	for _, part := range parts {
		candidates = append(candidates, regions.Search(&cnregion.SearchOptions{
			Text:  part,
			Level: id.Province | id.City | id.County,
			Max:   8,
		})...)
	}
	candidates = append(candidates, regions.Search(&cnregion.SearchOptions{
		Text:  location,
		Level: id.Province | id.City | id.County,
		Max:   8,
	})...)
	for _, candidate := range candidates {
		if regionNameMatches(location, candidate) {
			return candidate
		}
	}
	return nil
}

func loadRegionData() *cnregion.Version {
	regionDataOnce.Do(func() {
		regionData, _ = data.Embed(" ")
	})
	return regionData
}

func regionNameMatches(location string, region cnregion.Region) bool {
	if region == nil {
		return false
	}
	for _, name := range []string{region.Name(), region.FullName(), compactRegionName(region)} {
		name = strings.TrimSpace(name)
		if name != "" && strings.Contains(location, name) {
			return true
		}
	}
	return false
}

func compactRegionName(region cnregion.Region) string {
	if region == nil {
		return ""
	}
	parts := strings.Fields(region.FullName())
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", "市辖区", "县":
			continue
		default:
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		return strings.TrimSpace(region.Name())
	}
	return strings.Join(filtered, " ")
}

func trimRegionSuffix(location, regionName string) string {
	location = strings.TrimSpace(location)
	regionName = strings.TrimSpace(regionName)
	if location == "" || regionName == "" {
		return location
	}
	if location == regionName {
		return ""
	}
	if strings.HasPrefix(location, regionName+" ") {
		return strings.TrimSpace(strings.TrimPrefix(location, regionName))
	}
	return location
}
