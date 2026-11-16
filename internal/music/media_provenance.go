package music

// Media provenance is separate from the canonical Track.Source. None of these
// fields authorizes a client-selected object key or asserts recording identity.
type mediaProvenance struct {
	MediaSource   string `json:"mediaSource,omitempty"`
	OriginTrackID string `json:"originTrackID,omitempty"`
	MatchMethod   string `json:"matchMethod,omitempty"`
	Verification  string `json:"verification,omitempty"`
}

func provenanceOf(r Resolution) mediaProvenance {
	return mediaProvenance{r.MediaSource, r.OriginTrackID, r.MatchMethod, r.Verification}
}
func (p mediaProvenance) apply(r *Resolution) {
	r.MediaSource, r.OriginTrackID, r.MatchMethod, r.Verification = p.MediaSource, p.OriginTrackID, p.MatchMethod, p.Verification
}
func (p mediaProvenance) validUNM(id string) bool {
	return p.MediaSource == "unm:pyncmd" && p.OriginTrackID == id && p.MatchMethod == "provider_external_id" && p.Verification == "id_and_duration"
}
func durationNearCanonical(actual, canonical int64) bool {
	if actual <= 0 || canonical <= 0 || canonical > 86400000 {
		return false
	}
	tolerance := canonical * 3 / 100
	if tolerance < 1500 {
		tolerance = 1500
	}
	if tolerance > 5000 {
		tolerance = 5000
	}
	delta := actual - canonical
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
