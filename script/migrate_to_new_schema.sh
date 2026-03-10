#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SQL_DIR="${SCRIPT_DIR}/sql"
PSQL_BIN="${PSQL_BIN:-psql}"
PG_DUMP_BIN="${PG_DUMP_BIN:-pg_dump}"

OLD_SCHEMA="${OLD_SCHEMA:-betago}"
NEW_SCHEMA="${NEW_SCHEMA:-}"
STRICT="${STRICT:-1}"
RESET_TARGET_SCHEMA="${RESET_TARGET_SCHEMA:-0}"
SKIP_SCHEMA_DUMP="${SKIP_SCHEMA_DUMP:-0}"
SKIP_COPY="${SKIP_COPY:-0}"
SKIP_VALIDATE="${SKIP_VALIDATE:-0}"

ACTIVE_TABLE_CANDIDATES=(
  "copy_writing_customs"
  "copy_writing_generals"
  "dynamic_configs"
  "function_enablings"
  "imitate_rate_customs"
  "interaction_stats"
  "lark_imgs"
  "msg_trace_logs"
  "permission_grants"
  "private_modes"
  "prompt_template_args"
  "quote_reply_msg_customs"
  "quote_reply_msgs"
  "react_image_meterials"
  "repeat_words_rate_customs"
  "repeat_words_rates"
  "scheduled_tasks"
  "sticker_mappings"
  "template_versions"
  "todo_items"
)

usage() {
  cat <<'EOF'
Usage:
  DSN='postgres://user:pass@host:5432/dbname?sslmode=disable' \
  NEW_SCHEMA=betago_clean \
  ./script/migrate_to_new_schema.sh

Environment variables:
  DSN                  PostgreSQL connection string. Required.
  OLD_SCHEMA           Source schema. Default: betago
  NEW_SCHEMA           Target schema. Required.
  STRICT               1 = fail if any candidate table is missing in source schema. Default: 1
  RESET_TARGET_SCHEMA  1 = drop and recreate target schema before migration. Default: 0
  SKIP_SCHEMA_DUMP     1 = skip pg_dump schema creation step. Default: 0
  SKIP_COPY            1 = skip data copy step. Default: 0
  SKIP_VALIDATE        1 = skip row count and seed validation step. Default: 0
  PSQL_BIN             Override psql binary. Default: psql
  PG_DUMP_BIN          Override pg_dump binary. Default: pg_dump

Notes:
  - Stop application writes before running this script.
  - The script only migrates the active PostgreSQL tables used by current runtime code.
  - After a successful run, switch app config search_path to the target schema.
EOF
}

log() {
  printf '[schema-migrate] %s\n' "$*"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing command: %s\n' "$1" >&2
    exit 1
  fi
}

join_by_comma() {
  local out="" item
  for item in "$@"; do
    if [[ -n "$out" ]]; then
      out+=","
    fi
    out+="$item"
  done
  printf '%s' "$out"
}

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ -z "${DSN:-}" ]]; then
  printf 'DSN is required\n' >&2
  usage
  exit 1
fi

if [[ -z "${NEW_SCHEMA}" ]]; then
  printf 'NEW_SCHEMA is required\n' >&2
  usage
  exit 1
fi

if [[ "${OLD_SCHEMA}" == "${NEW_SCHEMA}" ]]; then
  printf 'OLD_SCHEMA and NEW_SCHEMA must be different\n' >&2
  exit 1
fi

require_cmd "${PSQL_BIN}"
require_cmd "${PG_DUMP_BIN}"

candidate_tables_csv="$(join_by_comma "${ACTIVE_TABLE_CANDIDATES[@]}")"

log "source schema: ${OLD_SCHEMA}"
log "target schema: ${NEW_SCHEMA}"
log "strict mode: ${STRICT}"
log "reset target schema: ${RESET_TARGET_SCHEMA}"
log "candidate active tables: ${candidate_tables_csv}"

mapfile -t existing_tables < <(
  "${PSQL_BIN}" "${DSN}" -X -A -t -v ON_ERROR_STOP=1 \
    -v old_schema="${OLD_SCHEMA}" \
    -v candidate_table_list="${candidate_tables_csv}" <<'SQL'
select table_name
from information_schema.tables
where table_schema = :'old_schema'
  and table_name = any(string_to_array(:'candidate_table_list', ','))
order by table_name;
SQL
)

if [[ ${#existing_tables[@]} -eq 0 ]]; then
  printf 'no active candidate tables found in source schema %s\n' "${OLD_SCHEMA}" >&2
  exit 1
fi

declare -A existing_map=()
for table_name in "${existing_tables[@]}"; do
  existing_map["${table_name}"]=1
done

missing_tables=()
for table_name in "${ACTIVE_TABLE_CANDIDATES[@]}"; do
  if [[ -z "${existing_map[${table_name}]:-}" ]]; then
    missing_tables+=("${table_name}")
  fi
done

log "source schema existing active tables: $(join_by_comma "${existing_tables[@]}")"
if [[ ${#missing_tables[@]} -gt 0 ]]; then
  log "source schema missing candidate tables: $(join_by_comma "${missing_tables[@]}")"
  if [[ "${STRICT}" == "1" ]]; then
    printf 'strict mode enabled; aborting because candidate tables are missing in source schema\n' >&2
    exit 1
  fi
fi

target_table_count="$(
  "${PSQL_BIN}" "${DSN}" -X -A -t -v ON_ERROR_STOP=1 \
    -v new_schema="${NEW_SCHEMA}" <<'SQL'
select count(*)
from information_schema.tables
where table_schema = :'new_schema';
SQL
)"

if [[ "${RESET_TARGET_SCHEMA}" == "1" ]]; then
  log "dropping target schema ${NEW_SCHEMA}"
  "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1 -v new_schema="${NEW_SCHEMA}" <<'SQL'
select format('drop schema if exists %I cascade', :'new_schema') \gexec
select format('create schema %I', :'new_schema') \gexec
SQL
elif [[ "${target_table_count}" != "0" ]]; then
  printf 'target schema %s already contains %s tables; rerun with RESET_TARGET_SCHEMA=1 or choose a new schema\n' "${NEW_SCHEMA}" "${target_table_count}" >&2
  exit 1
else
  log "creating target schema ${NEW_SCHEMA}"
  "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1 -v new_schema="${NEW_SCHEMA}" <<'SQL'
select format('create schema if not exists %I', :'new_schema') \gexec
SQL
fi

if [[ "${SKIP_SCHEMA_DUMP}" != "1" ]]; then
  log "dumping source table DDL into target schema"
  dump_args=(--schema-only)
  for table_name in "${existing_tables[@]}"; do
    dump_args+=(-t "${OLD_SCHEMA}.${table_name}")
  done

  "${PG_DUMP_BIN}" "${DSN}" "${dump_args[@]}" \
    | sed "s/${OLD_SCHEMA}\./${NEW_SCHEMA}\./g" \
    | "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1
fi

existing_tables_csv="$(join_by_comma "${existing_tables[@]}")"

if [[ "${SKIP_COPY}" != "1" ]]; then
  log "copying active table data"
  "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1 \
    -v old_schema="${OLD_SCHEMA}" \
    -v new_schema="${NEW_SCHEMA}" \
    -v table_list="${existing_tables_csv}" \
    -f "${SQL_DIR}/copy_active_tables.sql"

  log "syncing target schema sequences"
  "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1 -v new_schema="${NEW_SCHEMA}" <<'SQL'
do $$
declare
  target_schema text := :'new_schema';
  r record;
  max_value bigint;
begin
  for r in
    select table_name, column_name
    from information_schema.columns
    where table_schema = target_schema
      and column_default like 'nextval(%'
    order by table_name, ordinal_position
  loop
    execute format(
      'select max(%I) from %I.%I',
      r.column_name,
      target_schema,
      r.table_name
    ) into max_value;

    if max_value is null then
      execute format(
        'select setval(pg_get_serial_sequence(%L, %L), 1, false)',
        format('%I.%I', target_schema, r.table_name),
        r.column_name
      );
    else
      execute format(
        'select setval(pg_get_serial_sequence(%L, %L), %s, true)',
        format('%I.%I', target_schema, r.table_name),
        r.column_name,
        max_value
      );
    end if;
  end loop;
end $$;
SQL
fi

if [[ "${SKIP_VALIDATE}" != "1" ]]; then
  log "validating copied row counts and prompt seeds"
  "${PSQL_BIN}" "${DSN}" -X -v ON_ERROR_STOP=1 \
    -v old_schema="${OLD_SCHEMA}" \
    -v new_schema="${NEW_SCHEMA}" \
    -v table_list="${existing_tables_csv}" \
    -f "${SQL_DIR}/validate_active_tables.sql"
fi

log "migration finished"
log "next step: set db_config.search_path to ${NEW_SCHEMA},public and start the app against the new schema"
