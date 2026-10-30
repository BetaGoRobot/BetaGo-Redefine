package memesource

import (
	"context"
	"errors"
	"testing"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/application/lark/memes"
	"github.com/BetaGoRobot/BetaGo-Redefine/internal/infrastructure/memestore"
)

func emptyExamplesFixture(t *testing.T) memes.EncodedWiki {
	t.Helper()
	r, err := (&fakeCompletion{}).Complete(context.Background(), memes.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var encoded memes.EncodedWiki
	if err = memes.DecodeStrict(r.JSON, &encoded); err != nil {
		t.Fatal(err)
	}
	encoded.Cards[0].Full.Examples = memes.Examples{Positive: []memes.Example{}, NearMiss: []memes.Example{}}
	return encoded
}

func TestEmptyExamplesEncodingKeepsValidFingerprintsAfterJSONFreeze(t *testing.T) {
	records, err := compileContentIdentities(testSource(), emptyExamplesFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if _, err = memes.CompileCard(record.Full, record.Projection); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyEmptyExamplesCacheOnlyRepairsProvenDerivedHashes(t *testing.T) {
	source := testSource()
	legacy, err := memes.CompileWikiEncoding(source.WikiSource(), emptyExamplesFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = memes.CompileCard(legacy[0].Full, legacy[0].Projection); err == nil {
		t.Fatal("fixture must reproduce the old stale hash")
	}
	store := newMemoryStore()
	saved := SavedEncoding{SourceStorageID: source.StorageID(), EncodingHash: "profile", Records: legacy, Call: memes.CallMetadata{Model: "original-provider-receipt"}}
	if _, err = store.Put(context.Background(), memestore.Card, "cache", saved); err != nil {
		t.Fatal(err)
	}
	reused, found, err := loadEncoding(context.Background(), store, "cache", source, "profile")
	if err != nil || !found {
		t.Fatalf("legacy reuse failed: found=%t err=%v", found, err)
	}
	if reused.Call.Model != saved.Call.Model {
		t.Fatal("provider provenance changed")
	}
	if _, err = memes.CompileCard(reused.Records[0].Full, reused.Records[0].Projection); err != nil {
		t.Fatal(err)
	}
	if len(store.order) != 1 {
		t.Fatal("readback must not rewrite immutable cache")
	}
	legacy[0].Fingerprint = "unrelated-corruption"
	saved.Records = legacy
	if _, err = store.Put(context.Background(), memestore.Card, "corrupt", saved); err != nil {
		t.Fatal(err)
	}
	if _, _, err = loadEncoding(context.Background(), store, "corrupt", source, "profile"); !errors.Is(err, ErrStorage) {
		t.Fatalf("unknown corruption accepted: %v", err)
	}
}
