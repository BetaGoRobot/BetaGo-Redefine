# Candidate Ark Stage Engine Design

## Scope

Implement the production `CandidateStageEngine` used by the side-effect-free
Candidate lane. Task 5 will only instantiate this engine, build an anchored safe
tool registry, schedule the run, and persist the resulting lane output.

The engine must never expose the serving tool registry to Ark and must never
deliver or update Lark messages or cards.

## Construction and Ark seam

`conversationeval/candidate_ark.go` exports:

- `ArkCandidateEngineConfig`, containing a required model ID, an
  `llmusage.Scope`, and a bounded tool-round limit;
- a production constructor whose default completion function calls
  `ark_dal.ResponseTextWithCache` with strict JSON output;
- an injected-completion constructor for deterministic tests.

Every completion request identifies the Candidate stage. The Ark cache scene
and usage source include that stage, and every call uses the configured model
ID. The production completion wrapper passes no Ark tools and never calls
`WithTools`.

## Four stages

Activation returns a strict object describing active or silent participation.
Relevance returns strict `JoinDecision` and `TopicRelation` enums. Both raw
objects are retained in the lane output.

Context selection sends Ark a candidate list whose entries contain a stable ID,
the original bucket identity (`messages`, `retrieved`, `events`, or
`excluded`), token count, causal timestamp, and content. Ark returns selected
IDs. The engine rejects unknown IDs, duplicate IDs, items after the anchor, and
selections exceeding the token budget. It rebuilds each original snapshot
bucket and the excluded collection without guessing from `Source`; promoted
excluded items retain their original identity data. Non-selected items receive
an explicit Candidate exclusion reason.

Draft returns a strict decision: `reply`, `skip`, or `tool`. A skip has empty
reply text and no tool calls. A tool decision contains structured tool names and
object arguments. Calls run sequentially and exclusively through
`CandidateDraftInput.Tools.Invoke`. Completed observations are sent back to Ark
for the next draft round. If Ark requests an unknown or unsafe tool, produces
invalid arguments, or exceeds the configured round limit, the stage fails.
The Runner's invocation recorder preserves completed observations in
`LaneOutput.ToolPlanJSON` on that failure.

## Usage and errors

`CandidateRunner` attaches one request-local `llmusage.Collector` to the context
shared by all stages. On successful completion, recorded usage is aggregated
into token usage JSON. If no records were emitted, the existing
`CandidateDraft.TokenUsageJSON` remains the compatibility fallback.

Stage parsing, selection, tool, and round-limit failures propagate through the
existing Runner error path, producing a Candidate shadow `LaneOutput` with the
corresponding stage in `ErrorJSON`. Candidate never invokes a delivery sink.

## Verification

Tests use an injected completion function to cover:

- four real stages, model ID, stage-specific cache scene/source, and usage;
- bucket-preserving context selection and every explicit context rejection;
- reply, skip, structured tool feedback, unsafe tool rejection, and round cap;
- completed observation retention on failure;
- the production constructor selecting the Ark wrapper without network access;
- no delivery interface or serving-registry fallback.
