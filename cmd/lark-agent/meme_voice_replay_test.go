package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/BetaGoRobot/BetaGo-Redefine/internal/agent/memes"
)

// TestNextMemeVoiceModelReplay exercises both welcome participation and silence
// through the same native engine, 74 tool schemas, isolated PostgreSQL and fake
// delivery/business adapters as TestNextDialogueModelReplay. Only the answer
// model is real. It never sends to a group or reads external source data.
//
// Opt in with NEXT_MEME_VOICE_REPLAY=1 and the existing NEXT_REPLAY_CONFIG (or
// NEXT_REPLAY_BASE_URL/API_KEY/MODEL/PROTOCOL/SERVICE_TIER),
// NEXT_REPLAY_TEST_DSN_FILE and NEXT_DIALOGUE_REPLAY_OUTPUT settings. The existing
// NEXT_DIALOGUE_REPLAY_CONSTRUCT_ONLY=1 builds fixtures without provider calls.
// Each scenario retains the fixture's 8-call ceiling and a 90-second deadline.
// A passing structural check does not replace human judgment of the saved jokes.
func TestNextMemeVoiceModelReplay(t *testing.T) {
	if os.Getenv("NEXT_MEME_VOICE_REPLAY") != "1" {
		t.Skip("set NEXT_MEME_VOICE_REPLAY=1 for isolated native real-model meme voice replay")
	}
	output := os.Getenv("NEXT_DIALOGUE_REPLAY_OUTPUT")
	if output == "" {
		t.Fatal("NEXT_DIALOGUE_REPLAY_OUTPUT is required")
	}
	ep := replayEndpoint(t)
	dsn := replayDSN(t)
	library, err := memes.Default()
	if err != nil {
		t.Fatal("cannot load actual embedded meme corpus")
	}
	if library.Metadata().Count != 1477 {
		t.Fatal("meme voice replay requires the preserved 1477-record corpus")
	}
	const prizeInput = "我中了蒋介石徽章！"
	prizeCandidates := library.Retrieve(prizeInput)
	foundPrizeReference := false
	for _, candidate := range prizeCandidates {
		if candidate.ID == "reference:A01" && strings.Contains(candidate.Meaning, "中大蒋") {
			foundPrizeReference = true
		}
	}
	if !foundPrizeReference {
		t.Fatal("actual local retrieval did not supply the recognizable prize homophone")
	}
	report := struct {
		replayReport
		ContractRevision   int
		AutomaticPassScope string
		MemeCorpus         memes.Metadata
		PrizeCandidates    []memes.Candidate
		ReviewCriteria     map[string]string
		ManualReviewStatus map[string]string
	}{
		replayReport:       replayReport{Version: 1, Model: ep.Model, StartedAt: time.Now().UTC(), ToolCount: 74, ConstructionOnly: os.Getenv("NEXT_DIALOGUE_REPLAY_CONSTRUCT_ONLY") == "1", Scenarios: []*replayScenario{}},
		ContractRevision:   2,
		AutomaticPassScope: "Native execution, reply/silence contracts, the prize homophone and factual relevance only. Humor quality and ambient timing remain pending manual review even when Passed is true.",
		MemeCorpus:         library.Metadata(), PrizeCandidates: prizeCandidates,
		ReviewCriteria:     map[string]string{},
		ManualReviewStatus: map[string]string{},
	}
	// Register before running scenarios, so failed plans, raw model replies,
	// provider-call counts and native retry counts are retained as evidence too.
	t.Cleanup(func() {
		report.FinishedAt = time.Now().UTC()
		report.Passed = !t.Failed()
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(output, append(b, '\n'), 0600) != nil {
			t.Error("cannot write meme voice replay report")
		}
	})
	tests := []struct {
		name, review string
		manual       bool
		run          func(*testing.T, *replayFixture)
	}{
		{
			name:   "direct_prize_homophone",
			review: "Recognizably preserves the retrieved 中大蒋 homophone in a natural short reply; the whole line need not match the reference.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				turn := f.turn(t, prizeInput, true)
				f.wantReply(t, turn)
				memeVoiceWantPrizeHomophone(t, turn)
			},
		},
		{
			name:   "addressed_blindbox_followup",
			review: "The observed follow-up text is explicitly addressed, so a substantive reply is required. Manually judge whether it continues the playful prize context or treats the box as a real product catalog; no phrase blacklist decides that judgment.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				first := f.turn(t, prizeInput, true)
				f.wantReply(t, first)
				memeVoiceWantPrizeHomophone(t, first)
				memeVoiceWantSubstantiveReply(t, f, f.turn(t, "除了委员长，盲盒还有什么款", true))
			},
		},
		{
			name:   "serious_official_product_after_joke",
			review: "After an actual direct prize-pun reply, the addressed follow-up explicitly asks for official assortment and pricing because the speaker intends to buy a box. Require a durable reply; manually verify that it recognizes the real purchase intent and does not invent official inventory or prices when the isolated adapters provide no external product data. Inspect the saved raw tool calls and text; no exact phrase or special seriousness keyword is required.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				first := f.turn(t, prizeInput, true)
				f.wantReply(t, first)
				memeVoiceWantPrizeHomophone(t, first)
				f.wantReply(t, f.turn(t, "这套徽章盲盒的官方款式和售价是多少？我准备买一盒。", true))
			},
		},
		{
			name:   "unaddressed_playful_reply_continuation",
			review: "Humans explicitly establish a fictional historical-figure blind-box game, then the current question replies to the delivered bot joke without an at-mention. Require one substantive reply; manually judge whether it actually plays along rather than refusing or fetching a product list. No particular invented figure or joke is required.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				f.human(t, "alice", "我们沿着这个梗编个民国人物盲盒，大家接着编。", false)
				f.seedReply(t, prizeInput, "真·中蒋了")
				memeVoiceWantSubstantiveReply(t, f, f.memeVoiceReplyTurn(t, "除了委员长，盲盒还有什么款", false))
			},
		},
		{
			name:   "ambiguous_unaddressed_physical_blindbox",
			review: "This raw follow-up has no at-mention, no reply parent and no explicit fictional-play setup. A real blind-box assortment interpretation, lookup, honest uncertainty or observation is allowed. Manually distinguish that ambiguity from the explicitly invited fictional game; do not label uncertainty as a humor failure by default.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				f.seedReply(t, prizeInput, "真·中蒋了")
				f.turn(t, "除了委员长，盲盒还有什么款", false)
			},
		},
		{
			name:   "fresh_ambient_prize_homophone",
			review: "Record the fresh human-to-human blind-box opportunity without forcing participation. Manually judge the choice to speak or observe and, if it speaks, recognition and natural use of the available homophone. The old prompt also replied here; a reply alone does not demonstrate an improvement.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				f.human(t, "alice", "刚到的盲盒拆开看看，谁先开？", false)
				f.human(t, "member", "我先，里面居然是人物徽章。", false)
				f.turn(t, prizeInput, false)
			},
		},
		{
			name:   "fresh_ambient_grounded_teasing",
			review: "Record the five-minute deployment versus wrong-branch-until-3am opportunity without forcing participation. Manually judge timing, relevance and friendly teasing if it replies. The old prompt also replied here; a reply alone does not demonstrate an improvement.",
			manual: true,
			run: func(t *testing.T, f *replayFixture) {
				f.human(t, "alice", "今晚这个上线进度也太离谱了。", false)
				f.turn(t, "他说五分钟上线，结果改错分支修到凌晨三点", false)
			},
		},
		{
			name:   "frequency_complaint_after_four_replies",
			review: "After four prior owned replies, third-person feedback about reply frequency must observe with no reaction, tool or delivery.",
			run: func(t *testing.T, f *replayFixture) {
				for i, answer := range []string{"这锅今天又轮到你背了", "你这进度条比下载器还会装忙", "行，先让它跑一会儿", "刚说完它就报错，挺会挑时候"} {
					f.seedReply(t, fmt.Sprintf("看看刚才这个情况 %d", i), answer)
				}
				f.wantObserve(t, f.turn(t, "他回复频率怎么这么高", false))
			},
		},
		{
			name:   "historical_birth_year_without_forced_meme",
			review: "Answer the actual historical question with the year 1887; mentioning the same historical person alone must not force a prize or blind-box joke.",
			run: func(t *testing.T, f *replayFixture) {
				turn := f.turn(t, "蒋介石哪年出生", true)
				f.wantReply(t, turn)
				text := replayText(turn.Plan)
				if !strings.Contains(text, "1887") && !strings.Contains(text, "一八八七") {
					t.Error("historical question did not receive the relevant birth year")
				}
				for _, forcedMeme := range []string{"中大蒋", "中奖", "盲盒", "徽章"} {
					if strings.Contains(text, forcedMeme) {
						t.Error("historical fact question acquired an unrelated prize joke")
					}
				}
			},
		},
		{
			name:   "repeated_laughter_without_new_hook",
			review: "Once the bot has joined a joke and humans only continue laughing, observe without reaction or another performance.",
			run: func(t *testing.T, f *replayFixture) {
				f.seedReply(t, prizeInput, "恭喜你中大蒋了。")
				f.human(t, "alice", "哈哈哈哈", false)
				f.human(t, "member", "哈哈哈哈哈", false)
				f.wantObserve(t, f.turn(t, "哈哈哈哈", false))
			},
		},
	}
	for _, test := range tests {
		report.ReviewCriteria[test.name] = test.review
		if test.manual {
			report.ManualReviewStatus[test.name] = "not_run"
		}
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			scenario := &replayScenario{Name: test.name, Turns: []*replayTurn{}}
			report.Scenarios = append(report.Scenarios, scenario)
			t.Cleanup(func() { scenario.Passed = !t.Failed() })
			f := newReplayFixture(t, ctx, dsn, ep, scenario)
			t.Cleanup(func() {
				scenario.ModelCalls = f.model.calls
				scenario.BindingCalls = f.bindingCalls
				scenario.PrivateForms = len(f.private.bindings)
				scenario.Reactions = f.group.reactions
			})
			if report.SchemaDigest == "" {
				report.SchemaDigest = f.model.schemaDigest
			}
			if !report.ConstructionOnly {
				if test.manual {
					report.ManualReviewStatus[test.name] = "pending"
				}
				test.run(t, f)
			}
		})
	}
}

// Only this turn receives the parent. Restore the zero-default fixture field
// even if the native turn fails, so later input cannot inherit a reply relation.
func (f *replayFixture) memeVoiceReplyTurn(t *testing.T, text string, addressed bool) *replayTurn {
	t.Helper()
	if len(f.sender.sent) == 0 || f.sender.sent[len(f.sender.sent)-1].MessageID == "" {
		t.Fatal("reply continuation requires an actual delivered bot message")
	}
	previous := f.replyParent
	f.replyParent = f.sender.sent[len(f.sender.sent)-1].MessageID
	defer func() { f.replyParent = previous }()
	return f.turn(t, text, addressed)
}

func memeVoiceWantSubstantiveReply(t *testing.T, f *replayFixture, turn *replayTurn) {
	t.Helper()
	f.wantReply(t, turn)
	if turn.Plan == nil {
		return
	}
	// This catches empty/reaction-only/laughter-only output, not semantic humor
	// quality. A contentful refusal can pass this check and still fail review.
	letters := 0
	for _, message := range turn.Plan.Messages {
		for _, r := range message.Text {
			if (unicode.IsLetter(r) || unicode.IsDigit(r)) && !strings.ContainsRune("哈呵嘿嘻啊呀哦嗯嗷噗", r) {
				letters++
			}
		}
	}
	if letters < 3 {
		t.Error("explicit continuation needs one substantive reply, not only laughter or a reaction")
	}
}

func memeVoiceWantPrizeHomophone(t *testing.T, turn *replayTurn) {
	t.Helper()
	if !memeVoiceHasPrizeHomophone(replayText(turn.Plan)) {
		t.Error("prize reply lost the recognizable 中大蒋 form supplied by the actual local library")
	}
}

func memeVoiceHasPrizeHomophone(text string) bool {
	// Permit harmless spacing/punctuation while preserving the recognizable
	// phrase and its aspect/classifier variants. Do not prescribe a whole line
	// or accept merely 真·中蒋了, which loses the 大 in the prize homophone.
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return r
	}, text)
	for _, form := range []string{"中大蒋", "中了大蒋", "中了个大蒋"} {
		if strings.Contains(compact, form) {
			return true
		}
	}
	return false
}

func TestMemeVoicePrizeHomophoneRecognition(t *testing.T) {
	for _, test := range []struct {
		name, text string
		want       bool
	}{
		{name: "core", text: "恭喜你中大蒋了。", want: true},
		{name: "aspect", text: "恭喜你，中了大蒋！", want: true},
		{name: "aspect_and_classifier", text: "恭喜，你这算中大奖了——中了个大蒋", want: true},
		{name: "spacing_and_punctuation", text: "恭喜，中 · 大 · 蒋！", want: true},
		{name: "missing_prize_syllable", text: "真·中蒋了"},
		{name: "generic_prize", text: "恭喜你中大奖了！"},
		{name: "explanation_only", text: "谐音是大蒋"},
		{name: "empty", text: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := memeVoiceHasPrizeHomophone(test.text); got != test.want {
				t.Errorf("memeVoiceHasPrizeHomophone(%q) = %t, want %t", test.text, got, test.want)
			}
		})
	}
}
