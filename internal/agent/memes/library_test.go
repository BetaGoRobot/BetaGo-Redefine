package memes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddedCorpusCanRetrieveItsActualRecords(t *testing.T) {
	library, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	meta := library.Metadata()
	sum := sha256.Sum256(embeddedLibrary)
	if meta.Count != 1477 || meta.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("unexpected embedded repertoire metadata: %+v", meta)
	}
	got := library.Retrieve("今天又加班到凌晨，打工人实在顶不住了")
	for _, candidate := range got {
		if candidate.ID == "chime:打工人" && strings.Contains(candidate.Meaning, "上班族") && candidate.SourceKind == "chime_dataset" {
			return
		}
	}
	t.Fatal("an actual embedded record was not available from the current utterance")
}

func TestRetrievalDoesNotTurnSingleCharacterOrEntityMentionIntoPrizeJoke(t *testing.T) {
	library, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"蒋", "嗯", "的", "a"} {
		if got := library.Retrieve(query); len(got) != 0 {
			t.Fatalf("single character %q forced %d unrelated references", query, len(got))
		}
	}
	for _, candidate := range library.Retrieve("蒋介石是哪一年出生的？") {
		if candidate.ID == "reference:A01" {
			t.Fatal("mentioning Chiang alone promoted a prize-opening joke")
		}
	}
	for _, candidate := range library.Retrieve("拆盲袋这次抽到蒋介石，真是中大奖了，中大蒋了") {
		if candidate.ID == "reference:A01" {
			if len(candidate.WhenToUse) != 3 || !strings.Contains(candidate.WhenToUse[0], "盲") {
				t.Fatal("conditional reference lost its actual use conditions")
			}
			return
		}
	}
	t.Fatal("explicit prize wordplay did not expose the conditional reference")
}

func TestRetrievalDoesNotPromoteGenericDialogueFragments(t *testing.T) {
	library, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"感觉codex又变慢了",
		"@_user_1 你的回答让我眩晕",
		"@_user_1 所以其实，你这里关注的是「互动」？",
		"@某成员 你的回答让我眩晕",
	} {
		if got := library.Retrieve(query); len(got) != 0 {
			t.Errorf("ordinary dialogue %q promoted unrelated meme references: %v", query, got)
		}
	}
}

func TestRetrievalIsDeterministicBoundedAndCannotMutateCorpus(t *testing.T) {
	library, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	query := "打工人 小丑竟是我自己 绝绝子 不是X买不起，而是Y更有性价比 我释怀的笑了 炎过大侠 阎王点名 骨灰薯片"
	first := library.Retrieve(query)
	if len(first) == 0 || len(first) > 8 {
		t.Fatalf("candidate count outside bound: %d", len(first))
	}
	raw, err := json.Marshal(first)
	if err != nil || len(raw) > 6<<10 {
		t.Fatalf("display data exceeded 6 KiB: bytes=%d err=%v", len(raw), err)
	}
	if !reflect.DeepEqual(first, library.Retrieve(query)) {
		t.Fatal("same utterance produced different lexical retrieval")
	}
	first[0].Meaning = "mutated"
	for i := range first {
		if len(first[i].Examples) > 0 {
			first[i].Examples[0] = "mutated"
		}
		if len(first[i].WhenToUse) > 0 {
			first[i].WhenToUse[0] = "mutated"
		}
	}
	again, _ := json.Marshal(library.Retrieve(query))
	if strings.Contains(string(again), "mutated") {
		t.Fatal("retrieval leaked mutable corpus backing storage")
	}
}

func TestPrizeFamilyRetrievesFromNaturalBlindBagWording(t *testing.T) {
	library, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"蒋介石是哪一年出生的？", "蒋介石和老蒋是同一个人吗", "这次盲盒我中奖了"} {
		for _, candidate := range library.Retrieve(query) {
			if candidate.ID == "reference:A01" {
				t.Fatalf("one condition alone admitted the prize/entity family: %q", query)
			}
		}
	}
	for _, query := range []string{"拆徽章盲袋抽到了蒋介石", "我打开了徽章盲袋，抽到的竟然是蒋介石"} {
		found := false
		for _, candidate := range library.Retrieve(query) {
			if candidate.ID == "reference:A01" {
				found = true
			}
		}
		if !found {
			t.Fatalf("natural prize-opening and entity context did not retrieve the conditional family: %q", query)
		}
	}
}

func TestMalformedCorpusFailsWithoutEchoingItsContents(t *testing.T) {
	for _, raw := range []string{
		`not-json-private-marker`,
		`[]`,
		`[{"id":"private-marker","title":"x"}]`,
		`[{"id":"same","title":"x","meaning":"x","source_kind":"public"},{"id":"same","title":"y","meaning":"y","source_kind":"public"}]`,
		`[{"id":"private-marker","title":"x","meaning":"x","source_kind":"public","retrieval_all":[[]]}]`,
		`[{"id":"private-marker","title":"x","meaning":"x","source_kind":"public","retrieval_all":[[""]]}]`,
	} {
		_, err := load([]byte(raw))
		if err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatalf("invalid corpus was accepted or contents exposed: %v", err)
		}
	}
}
