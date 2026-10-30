# meme-sync

`meme-sync` browses the public [Wiki API](https://cnmeme.wiki/developers/api), saves original list/detail data, encodes source-backed reference cards offline, and publishes the shared relation/emotion/keys/description collections in Milvus. Conversation retrieval uses those collections and does not contact the Wiki.

Run from the main workspace, or use its existing config by absolute path from a worktree:

```sh
GOTOOLCHAIN=go1.26.8 BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.0/bin/go run -tags=custom_skip_vips ./cmd/meme-sync \
  --dry-run --limit 1 --timeout 30s
```

`--dry-run` fetches only public source data. It does not load the local config, call a model, connect to the database, or write indexes. Its output lists source revisions, errors and pagination status.

An actual sync uses the existing master config's database DSN and Ark API key. Milvus uses its own meme settings and credentials. The CLI initializes Ark and the usage recorder without starting Lark or the bot:

```sh
GOTOOLCHAIN=go1.26.8 BETAGO_CONFIG_PATH=/mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  /root/.go/go1.26.0/bin/go run -tags=custom_skip_vips ./cmd/meme-sync \
  --config /mnt/RapidPool/workspace/BetaGo_v2/.dev/config.toml \
  --model deepseek-v4-1-flash-economy-261017 \
  --schema meme_reply --scope wiki --prefix memes --limit 1 \
  --generation wiki_sample_01 --timeout 5m
```

Use `--index-endpoint` to override the Milvus REST URL. It otherwise uses `[memes].search_endpoint`, then `http://betago-meme-milvus:19530`. Authentication comes from `[memes].search_username` and `search_password`; nonempty `MEME_INDEX_USERNAME` and `MEME_INDEX_PASSWORD` environment variables override those values. OpenSearch configuration is never inherited. Passwords have no command-line flag. `--index-insecure-tls` explicitly permits a self-hosted cluster's self-signed certificate. `--model`, `--embedding-model` and `--dimensions` control the encoder and index generation. The encoder defaults to the configured normal model. The runtime must point to the same database/schema, scope, prefix, embedding model and dimensions.

The catalog request supplies only `limit` and `offset`, never a conversation keyword or tag. The documented API supports page sizes 1–30. `--limit 0` browses all pages, while the default attempts at most 30 unique sources. `--offset` starts a selected batch; `next_offset` reports where a following batch begins. Remote updates can move entries between offset pages, so the importer deduplicates slugs within a run and reports `catalog_complete` separately.

Each generation contains exactly the selected batch. A later batch does not append to an earlier generation. Repeat the same offset/limit to resume failed work; exact source revisions and encoder model/prompt/schema versions reuse their saved encodings. To publish a complete corpus after small batches, run from offset 0 with limit 0; previously encoded immutable revisions are reused. The default generation ID is a deterministic corpus hash; an explicit generation ID is immutable and conflicts if its contents change.

Large imports can set `--source-workers 8 --model-concurrency 8 --timeout 6h` to run independent source encodings concurrently under the shared admission quota. The default is two source workers; library callers retain a one-worker default. Source preparation, successful card-cache writes, report aggregation and publication follow catalog order on one coordinator. A single failed source still prevents this generation from publishing or activating; successful earlier sources remain cached for a retry. The online runtime can keep its separate configured concurrency of two after the offline import finishes.

Public Wiki GETs default to one-second spacing and up to four retries for transient transport failures and HTTP 429/502/503/504. `--wiki-interval` and `--wiki-retries` control that policy. Backoff honors cancellation and Retry-After up to thirty seconds; a longer server instruction stops that request rather than retrying earlier. Permanent detail errors are reported, never replaced with catalog summaries.

`--source-cache-dir /data/meme-wiki-sources` optionally preserves public raw-detail snapshots across restarts. The key includes the source base URL and complete raw catalog item, so a changed catalog version fetches a new detail. Cache hits rebuild provenance and plain text from raw detail and verify the stored source revision; corrupt files are ignored and fetched again. Files publish by atomic rename. This cache is a resumable public snapshot, not a claim that an unchanged catalog entry's remote detail has been re-fetched. Dry runs ignore the cache option and never write it.

The CLI now emits `progress stage=wiki` for each page/detail and `progress stage=sync` for ordered source completion, publication and activation. These lines contain counts and public source identifiers, not source bodies, credentials or chat inputs. The final summary remains the authoritative activation result.

Source snapshots preserve the original title, summary, HTML, update timestamp, tags, cover, source video metadata, detail URL, raw API JSON and deterministic plain text. Cards point back to that immutable source revision. One entry may produce multiple use cards. The program attaches the original description and provenance, marks generated conditions `pending`, computes fingerprints and validates the cards and projections. This status expresses unreviewed inference and does not imply human approval or suppress the references.

The encoder sends its schema through Ark Responses `text.format` using `json_schema`; the local validators still enforce the full contract. Invalid JSON structure or explicit enum, nonempty-field and array-size constraints may consume one repair call with the same schema, the exact original source input and the invalid output marked as untrusted data. Both call receipts and latencies appear in the report; the total remains at most two calls per source. A second format error stops that source and reports a schema field path without printing raw output. Transport errors, incomplete or oversized model responses, absent core semantics, invalid provenance and semantic compilation errors receive no repair retry. Technical IDs are normalized by the program; valid unique condition IDs and the v2 encoding cache profile stay unchanged.

Any detail, encoding or compilation error is reported per source and prevents this batch's publication and activation. Successfully saved sources and encodings remain available for a retry. Milvus stores vectors and fingerprint references; the complete immutable card and original description remain in scoped PostgreSQL facts without Milvus's string-size limit. Publication holds a PostgreSQL advisory transaction lock, records its operations and verifies collection schemas and records by readback, then saves a `backend=milvus` manifest and activates the generation. Legacy manifests retain their original backend identity. Errors leave the previous active generation selected. A failed publication can leave owned, inactive collections for an idempotent retry; it does not delete unrelated data.

The plain-text report includes source/card counts, encoding calls and latency, errors, pagination, the verified manifest and activation status. Use `--help` for all flags.
