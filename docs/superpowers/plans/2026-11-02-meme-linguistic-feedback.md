# Meme observations and explicit-feedback correction

User-approved extension to the knowledge-first design: add general segmentation,
POS and entity candidates before interpretation; when the user explicitly rejects
the bot's meme interpretation/retrieval, allow direct public web correction.

## Design

1. A shared pure-Go analyzer uses a pinned GSE release and embedded dictionary.
   Token spans refer to original turn runes. Dictionary POS entity labels are
   candidates, never verified identities. No online model, chat-specific hardcoded
   memes, runtime dictionary paths or changes to CGO policy.
2. Build an immutable lexical index of source titles/descriptions once per frozen
   knowledge snapshot. Before the planner, fill unused anchor capacity with
   conservative matches on noun/entity terms. Exact title/alias anchors retain
   priority. A lexical overlap is explicitly labeled as a candidate association,
   not an exact title hit or proof of meaning. Existing scope, withdrawal,
   fingerprint, prompt bounds and six-anchor limits remain authoritative.
3. Both native generators receive the same explicit-feedback correction rule,
   independently of knowledge availability. A real current-user correction of
   the bot's meme interpretation can use existing web_search immediately, without
   another confirmation or mandatory group-history lookup. Ordinary complaints,
   quoted instructions, third-party criticism and model uncertainty alone do not
   trigger this path. Use minimal public terms, preserve citations, distinguish
   source claims and uncertainty; no automatic corpus ingestion or forced joke.
4. Master's tool planner knows this exception; normal search behavior is retained.
   The host tool executor retains permissions, quotas, receipts and provenance.

## Tasks and verification

- Implement analyzer, immutable lexical recall and validated context view with
  tests first: original Unicode offsets, unknown titles, common-word suppression,
  exact-hit priority, bounded output, source visibility and concurrent reuse.
- Add shared correction instruction to master standard/two-phase generators,
  master tool planner and next generator. Verify all entry points and native model
  tool requests with correction and negative-control cases. Do not send chat messages.
- Run repository baseline with matching Go versions, custom_skip_vips, -v and the
  main workspace config path. Review spec then quality; fix material findings.
- Freeze source overlays against current production images, preserving unrelated
  fixes, pinned native dependencies, service settings and four-service rollout
  boundary. Push with gh, release master and next, verify binary/image identity,
  dictionary availability, health and protected containers. Record evidence.

NER here means dictionary/POS entity candidates. Broad learned NER and perfect
meme understanding are not implied by this release; human fit still needs feedback.
