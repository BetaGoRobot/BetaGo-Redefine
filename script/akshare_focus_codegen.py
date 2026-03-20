from __future__ import annotations

import argparse
import ast
import json
import re
import unicodedata
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

SOURCE_CATALOG_PATH = Path("docs/akshare/generated/akshare_api_catalog.json")
SDK_CATALOG_PATH = Path("docs/akshare/generated/akshare_sdk_catalog.json")
SDK_MARKDOWN_PATH = Path("docs/akshare/generated/akshare_sdk_catalog.md")
FOCUS_CATALOG_PATH = Path("docs/akshare/generated/akshare_focus_api_catalog.json")
FOCUS_MARKDOWN_PATH = Path("docs/akshare/generated/akshare_focus_api_catalog.md")
GO_OUTPUT_PATH = Path("internal/infrastructure/akshareapi/catalog_generated.go")

GO_KEYWORDS = {
    "break",
    "default",
    "func",
    "interface",
    "select",
    "case",
    "defer",
    "go",
    "map",
    "struct",
    "chan",
    "else",
    "goto",
    "package",
    "switch",
    "const",
    "fallthrough",
    "if",
    "range",
    "type",
    "continue",
    "for",
    "import",
    "return",
    "var",
}

DOMAIN_NAME_MAP = {
    "stock": "DomainStock",
    "gold": "DomainGold",
    "futures": "DomainFutures",
    "info": "DomainInfo",
}

ROW_FIELD_KIND_OVERRIDES = {
    "spot_hist_sge": {
        "date": "string",
        "open": "number",
        "close": "number",
        "low": "number",
        "high": "number",
    },
    "spot_quotations_sge": {
        "品种": "string",
        "时间": "string",
        "现价": "number",
        "更新时间": "string",
    },
    "stock_zh_a_minute": {
        "day": "string",
        "open": "string",
        "high": "string",
        "low": "string",
        "close": "string",
        "volume": "string",
        "amount": "string",
    },
    "stock_individual_info_em": {
        "item": "string",
        "value": "unknown",
    },
}


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def quote_go_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def normalize_doc_type(value: Any) -> str:
    return str(value or "").strip().lower()


def infer_value_kind(doc_type: Any, runtime_value: Any) -> str:
    if isinstance(runtime_value, bool):
        return "boolean"
    if isinstance(runtime_value, int) and not isinstance(runtime_value, bool):
        return "integer"
    if isinstance(runtime_value, float):
        return "number"
    if isinstance(runtime_value, str):
        return "string"
    if isinstance(runtime_value, (list, tuple)):
        return "array"
    if isinstance(runtime_value, dict):
        return "object"

    doc_type_value = normalize_doc_type(doc_type)
    if doc_type_value in {"str", "string"}:
        return "string"
    if doc_type_value in {"int", "int32", "int64", "integer"}:
        return "integer"
    if doc_type_value in {"float", "float32", "float64", "double", "number"}:
        return "number"
    if doc_type_value in {"bool", "boolean"}:
        return "boolean"
    if doc_type_value in {"list", "array"}:
        return "array"
    if doc_type_value in {"dict", "map"}:
        return "object"
    if doc_type_value in {"object"}:
        return "unknown"
    return "unknown"


def value_kind_constant(kind: str) -> str:
    return {
        "string": "ValueKindString",
        "integer": "ValueKindInteger",
        "number": "ValueKindNumber",
        "boolean": "ValueKindBoolean",
        "array": "ValueKindArray",
        "object": "ValueKindObject",
    }.get(kind, "ValueKindUnknown")


def split_identifier_tokens(value: str) -> list[str]:
    cleaned = re.sub(r"[^\w]+", " ", value, flags=re.UNICODE).strip()
    if not cleaned:
        return []
    return [token for token in re.split(r"[_\s]+", cleaned) if token]


def go_struct_field_name(name: str, used: set[str], index: int) -> str:
    raw_name = str(name or "").strip()
    tokens = split_identifier_tokens(raw_name)
    ascii_only = all(all(ord(char) < 128 for char in token) for token in tokens)

    if tokens and ascii_only:
        base = "".join(token[:1].upper() + token[1:] for token in tokens)
    else:
        base = "".join(char for char in raw_name if char.isalnum() or char == "_")
        base = base.replace("_", "")

    if not base:
        base = f"Field{index}"

    first = base[0]
    if not ("A" <= first <= "Z"):
        base = f"X{base}"

    if base.lower() in GO_KEYWORDS:
        base = f"X{base[:1].upper()}{base[1:]}"

    candidate = base
    if candidate in used:
        candidate = f"{base}{index}"
    suffix = index + 1
    while candidate in used:
        candidate = f"{base}{suffix}"
        suffix += 1
    used.add(candidate)
    return candidate


def go_method_name(interface_name: str) -> str:
    used: set[str] = set()
    return go_struct_field_name(interface_name, used, 1)


def literal_to_string(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, (str, int, float, bool)):
        return str(value)
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def ast_literal_value(node: ast.AST) -> Any:
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.List):
        return [ast_literal_value(item) for item in node.elts]
    if isinstance(node, ast.Tuple):
        return [ast_literal_value(item) for item in node.elts]
    if isinstance(node, ast.Dict):
        return {
            ast_literal_value(key): ast_literal_value(value)
            for key, value in zip(node.keys, node.values, strict=True)
        }
    raise ValueError("unsupported literal")


def extract_example_params(record: dict[str, Any]) -> dict[str, str]:
    example_code = str(record.get("example_code", "")).strip()
    interface_name = str(record.get("interface", "")).strip()
    if not example_code or not interface_name:
        return {}

    try:
        module = ast.parse(example_code)
    except SyntaxError:
        return {}

    for node in ast.walk(module):
        if not isinstance(node, ast.Call):
            continue
        func = node.func
        if isinstance(func, ast.Attribute) and func.attr != interface_name:
            continue
        if isinstance(func, ast.Name) and func.id != interface_name:
            continue
        if not isinstance(func, (ast.Attribute, ast.Name)):
            continue

        params: dict[str, str] = {}
        for keyword in node.keywords:
            if keyword.arg is None:
                continue
            try:
                params[keyword.arg] = literal_to_string(ast_literal_value(keyword.value))
            except ValueError:
                continue
        return params

    return {}


def extract_doc_sample_examples(record: dict[str, Any]) -> dict[str, str]:
    sample_data = str(record.get("sample_data", "")).strip()
    if not sample_data:
        return {}

    output_groups = record.get("output_groups", [])
    if not output_groups:
        return {}

    fields = [field.get("name", "").strip() for field in output_groups[0].get("fields", []) if field.get("name", "").strip()]
    if not fields:
        return {}

    for line in sample_data.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("..."):
            continue
        parts = stripped.split()
        if not parts or not parts[0].isdigit():
            continue
        values = parts[1:]
        if len(values) < len(fields):
            continue
        return {field: values[index] for index, field in enumerate(fields)}

    return {}


def existing_focus_tag_map(path: Path) -> dict[str, list[str]]:
    if not path.exists():
        return {}
    payload = load_json(path)
    mapping: dict[str, list[str]] = {}
    for record in payload.get("records", []):
        tags = record.get("focus_tags", [])
        if isinstance(tags, list):
            mapping[str(record.get("interface", ""))] = [str(tag) for tag in tags]
    return mapping


def classify_focus_tags(record: dict[str, Any]) -> list[str]:
    combined = " ".join(
        [
            str(record.get("interface", "")),
            str(record.get("category_slug", "")),
            str(record.get("page_url", "")),
            str(record.get("description", "")),
            " ".join(str(item) for item in record.get("headings", [])),
        ]
    ).lower()

    tags: list[str] = []

    if (
        str(record.get("category_slug", "")) == "stock"
        or str(record.get("interface", "")).startswith("stock_")
        or "股票" in combined
        or "a股" in combined
        or "/stock/" in combined
    ):
        tags.append("stock")

    if (
        str(record.get("category_slug", "")) in {"futures", "qhkc"}
        or str(record.get("interface", "")).startswith("futures_")
        or "期货" in combined
        or "席位" in combined
        or "仓单" in combined
        or "交割" in combined
        or "基差" in combined
    ):
        tags.append("futures")

    if (
        "黄金" in combined
        or "金价" in combined
        or "上海黄金交易所" in combined
        or str(record.get("interface", "")).endswith("_sge")
        or " gold " in f" {combined} "
    ):
        tags.append("gold")

    if (
        str(record.get("interface", "")).startswith("news_")
        or any(token in combined for token in ("资讯", "新闻", "公告", "报告", "快讯", "研报"))
        or str(record.get("interface", "")).endswith("_info")
        or "_news_" in str(record.get("interface", ""))
    ):
        tags.append("info")

    seen: set[str] = set()
    deduped: list[str] = []
    for tag in tags:
        if tag in DOMAIN_NAME_MAP and tag not in seen:
            seen.add(tag)
            deduped.append(tag)
    return deduped


def annotate_records(
    records: list[dict[str, Any]],
    existing_map: dict[str, list[str]],
) -> list[dict[str, Any]]:
    annotated: list[dict[str, Any]] = []
    for record in records:
        copied = dict(record)
        interface_name = str(copied.get("interface", ""))
        copied["focus_tags"] = existing_map.get(interface_name, classify_focus_tags(copied))
        annotated.append(copied)
    return annotated


def dedupe_records_by_interface(records: list[dict[str, Any]]) -> list[dict[str, Any]]:
    deduped: list[dict[str, Any]] = []
    seen: set[str] = set()
    for record in records:
        interface_name = str(record.get("interface", "")).strip()
        if not interface_name or interface_name in seen:
            continue
        seen.add(interface_name)
        deduped.append(record)
    return deduped


def build_domain_counts(records: list[dict[str, Any]]) -> dict[str, int]:
    counts = Counter()
    for record in records:
        for tag in record.get("focus_tags", []):
            counts[tag] += 1
    return {key: counts[key] for key in sorted(counts)}


def merge_output_fields(record: dict[str, Any]) -> list[dict[str, Any]]:
    merged: list[dict[str, Any]] = []
    seen: set[str] = set()
    for group in record.get("output_groups", []):
        for field in group.get("fields", []):
            name = str(field.get("name", "")).strip()
            if not name or name in seen:
                continue
            seen.add(name)
            merged.append(field)
    return merged


def render_catalog_markdown(title: str, records: list[dict[str, Any]], source_catalog: str) -> str:
    generated_at = datetime.now(timezone.utc).isoformat()
    lines = [
        f"# {title}",
        "",
        f"- Generated at: `{generated_at}`",
        f"- Source catalog: `{source_catalog}`",
        f"- Selected endpoints: `{len(records)}`",
        "",
        "## Domain Summary",
        "",
        "| Domain | Endpoint Count |",
        "|---|---:|",
    ]

    domain_counts = build_domain_counts(records)
    for domain, count in domain_counts.items():
        lines.append(f"| {domain} | {count} |")

    lines.extend(
        [
            "",
            "## Endpoints",
            "",
            "| API | Tags | Category | Deepest Heading |",
            "|---|---|---|---|",
        ]
    )
    for record in records:
        deepest_heading = record.get("headings", [])
        lines.append(
            "| `{}` | {} | {} | {} |".format(
                record.get("interface", ""),
                ", ".join(record.get("focus_tags", [])),
                record.get("category_slug", ""),
                deepest_heading[-1] if deepest_heading else "",
            )
        )
    return "\n".join(lines) + "\n"


def infer_field_kind(record: dict[str, Any], field: dict[str, Any], sample_examples: dict[str, str]) -> str:
    runtime_value = sample_examples.get(str(field.get("name", "")).strip())
    return infer_value_kind(field.get("type", ""), runtime_value)


def infer_row_field_kind(record: dict[str, Any], field: dict[str, Any], sample_examples: dict[str, str]) -> str:
    interface_name = str(record.get("interface", "")).strip()
    field_name = str(field.get("name", "")).strip()
    override_kind = ROW_FIELD_KIND_OVERRIDES.get(interface_name, {}).get(field_name)
    if override_kind:
        return override_kind

    doc_type = normalize_doc_type(field.get("type", ""))
    if doc_type in {"str", "string"}:
        return "string"
    if doc_type in {"int", "int32", "int64", "integer"}:
        return "integer"
    if doc_type in {"float", "float32", "float64", "double", "number"}:
        return "number"
    if doc_type in {"bool", "boolean"}:
        return "boolean"
    if doc_type in {"list", "array"}:
        return "array"
    if doc_type in {"dict", "map"}:
        return "object"

    runtime_value = sample_examples.get(field_name)
    if runtime_value is None:
        return "unknown"
    if isinstance(runtime_value, bool):
        return "boolean"
    if isinstance(runtime_value, (int, float)):
        return infer_value_kind("", runtime_value)
    if isinstance(runtime_value, str):
        lowered = runtime_value.strip().lower()
        if lowered in {"true", "false"}:
            return "boolean"
        if re.fullmatch(r"[+-]?\d+", runtime_value.strip()):
            return "integer"
        if re.fullmatch(r"[+-]?(?:\d+\.\d+|\d+)", runtime_value.strip()):
            return "number"
        return "string"
    return infer_value_kind("", runtime_value)


def go_type_for_kind(kind: str) -> str:
    return {
        "string": "string",
        "integer": "int64",
        "number": "float64",
        "boolean": "bool",
        "array": "[]any",
        "object": "any",
        "unknown": "any",
    }.get(kind, "any")


def render_row_struct(method_name: str, record: dict[str, Any]) -> str:
    fields = merge_output_fields(record)
    if not fields:
        return ""

    sample_examples = extract_doc_sample_examples(record)
    used: set[str] = set()
    lines = [f"type {method_name}Row struct {{"]
    for index, field in enumerate(fields, start=1):
        name = str(field.get("name", "")).strip()
        if not name:
            continue
        go_name = go_struct_field_name(name, used, index)
        go_type = go_type_for_kind(infer_row_field_kind(record, field, sample_examples))
        lines.append(f'\t{go_name} {go_type} `json:"{name}"`')
    lines.append("}")
    lines.append("")
    return "\n".join(lines)


def render_param_struct(method_name: str, record: dict[str, Any]) -> str:
    params = [item for item in record.get("input_params", []) if str(item.get("name", "")).strip() not in {"", "-"}]
    if not params:
        return ""

    used: set[str] = set()
    lines = [f"type {method_name}Params struct {{"]
    for index, param in enumerate(params, start=1):
        go_name = go_struct_field_name(str(param.get("name", "")), used, index)
        lines.append(f'\t{go_name} string `query:"{param.get("name", "")}"`')
    lines.append("}")
    lines.append("")
    return "\n".join(lines)


def render_param_specs(record: dict[str, Any]) -> list[str]:
    params = [item for item in record.get("input_params", []) if str(item.get("name", "")).strip() not in {"", "-"}]
    used: set[str] = set()
    specs: list[str] = []
    for index, param in enumerate(params, start=1):
        kind = infer_value_kind(param.get("type", ""), None)
        specs.append(
            '\t\t{Name: %s, GoName: %s, Kind: %s, Description: %s},'
            % (
                quote_go_string(str(param.get("name", ""))),
                quote_go_string(go_struct_field_name(str(param.get("name", "")), used, index)),
                value_kind_constant(kind),
                quote_go_string(str(param.get("description", ""))),
            )
        )
    return specs


def render_field_specs(record: dict[str, Any]) -> list[str]:
    sample_examples = extract_doc_sample_examples(record)
    specs: list[str] = []
    for field in merge_output_fields(record):
        kind = infer_field_kind(record, field, sample_examples)
        specs.append(
            '\t\t{Name: %s, Kind: %s, Description: %s},'
            % (
                quote_go_string(str(field.get("name", ""))),
                value_kind_constant(kind),
                quote_go_string(str(field.get("description", ""))),
            )
        )
    return specs


def render_tags(record: dict[str, Any]) -> str:
    tags = [DOMAIN_NAME_MAP[tag] for tag in record.get("focus_tags", []) if tag in DOMAIN_NAME_MAP]
    if not tags:
        return "[]Domain{}"
    return "[]Domain{%s}" % ", ".join(tags)


def render_endpoint_block(record: dict[str, Any]) -> tuple[str, str]:
    interface_name = str(record.get("interface", ""))
    method_name = go_method_name(interface_name)
    param_struct = render_param_struct(method_name, record)
    row_struct = render_row_struct(method_name, record)
    param_specs = render_param_specs(record)
    field_specs = render_field_specs(record)
    params = [item for item in record.get("input_params", []) if str(item.get("name", "")).strip() not in {"", "-"}]

    lines: list[str] = []
    if param_struct:
        lines.append(param_struct.rstrip())
        lines.append("")
    if row_struct:
        lines.append(row_struct.rstrip())
        lines.append("")

    lines.extend(
        [
            f"var Endpoint{method_name} = Endpoint{{",
            f"\tName: {quote_go_string(interface_name)},",
            f"\tMethodName: {quote_go_string(method_name)},",
            f"\tTags: {render_tags(record)},",
            f"\tSummary: {quote_go_string(record.get('headings', [])[-1] if record.get('headings') else '')},",
            f"\tDescription: {quote_go_string(str(record.get('description', '')))},",
            f"\tDocURL: {quote_go_string(str(record.get('page_url', '')))},",
            f"\tSourceURL: {quote_go_string(str(record.get('source_url', '')))},",
            f"\tTargetURL: {quote_go_string(str(record.get('target_url', '')))},",
            "\tParams: []ParamSpec{",
        ]
    )
    lines.extend(param_specs)
    lines.extend(
        [
            "\t},",
            "\tFields: []FieldSpec{",
        ]
    )
    lines.extend(field_specs)
    lines.extend(
        [
            "\t},",
            "}",
            "",
        ]
    )

    if params:
        lines.append(f"func (c *Client) {method_name}(ctx context.Context, params {method_name}Params) (Rows, error) {{")
        lines.append(f"\treturn c.callRows(ctx, Endpoint{method_name}, params)")
        lines.append("}")
    else:
        lines.append(f"func (c *Client) {method_name}(ctx context.Context) (Rows, error) {{")
        lines.append(f"\treturn c.callRows(ctx, Endpoint{method_name}, nil)")
        lines.append("}")
    lines.append("")
    return method_name, "\n".join(lines)


def render_go_catalog(records: list[dict[str, Any]]) -> str:
    sorted_records = sorted(dedupe_records_by_interface(records), key=lambda item: str(item.get("interface", "")))
    blocks: list[str] = []
    method_names: list[str] = []
    for record in sorted_records:
        method_name, block = render_endpoint_block(record)
        method_names.append(method_name)
        blocks.append(block.rstrip())

    lines = [
        "// Code generated by script/akshare_focus_codegen.py. DO NOT EDIT.",
        "",
        "package akshareapi",
        "",
        'import "context"',
        "",
        "\n\n".join(blocks),
        "",
        "func init() {",
        "\tregisterGeneratedEndpoints([]Endpoint{",
    ]
    for method_name in method_names:
        lines.append(f"\t\tEndpoint{method_name},")
    lines.extend(
        [
            "\t})",
            "}",
            "",
        ]
    )
    return "\n".join(lines)


def build_catalog_payload(
    records: list[dict[str, Any]],
    *,
    source_catalog_path: Path,
    source_payload: dict[str, Any],
) -> dict[str, Any]:
    deduped_records = dedupe_records_by_interface(records)
    return {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "source_catalog": str(source_catalog_path),
        "source_generated_at": source_payload.get("generated_at", ""),
        "server_metadata": source_payload.get("server_metadata", {}),
        "domain_counts": build_domain_counts(deduped_records),
        "records": deduped_records,
    }


def write_outputs(
    source_catalog_path: Path,
    sdk_json_path: Path,
    sdk_markdown_path: Path,
    focus_json_path: Path,
    focus_markdown_path: Path,
    go_output_path: Path,
) -> None:
    source_payload = load_json(source_catalog_path)
    source_records = list(source_payload.get("records", []))
    existing_map = existing_focus_tag_map(sdk_json_path)
    annotated_records = dedupe_records_by_interface(annotate_records(source_records, existing_map))
    focus_records = [record for record in annotated_records if record.get("focus_tags")]

    sdk_payload = build_catalog_payload(
        annotated_records,
        source_catalog_path=source_catalog_path,
        source_payload=source_payload,
    )
    focus_payload = build_catalog_payload(
        focus_records,
        source_catalog_path=source_catalog_path,
        source_payload=source_payload,
    )

    write_json(sdk_json_path, sdk_payload)
    sdk_markdown_path.write_text(
        render_catalog_markdown("AkShare SDK Catalog", annotated_records, str(source_catalog_path)),
        encoding="utf-8",
    )
    write_json(focus_json_path, focus_payload)
    focus_markdown_path.write_text(
        render_catalog_markdown("AkShare Focus Catalog", focus_records, str(source_catalog_path)),
        encoding="utf-8",
    )
    go_output_path.parent.mkdir(parents=True, exist_ok=True)
    go_output_path.write_text(render_go_catalog(annotated_records), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate focused AkShare SDK metadata and Go catalog.")
    parser.add_argument("--source-catalog", default=str(SOURCE_CATALOG_PATH))
    parser.add_argument("--sdk-json", default=str(SDK_CATALOG_PATH))
    parser.add_argument("--sdk-markdown", default=str(SDK_MARKDOWN_PATH))
    parser.add_argument("--focus-json", default=str(FOCUS_CATALOG_PATH))
    parser.add_argument("--focus-markdown", default=str(FOCUS_MARKDOWN_PATH))
    parser.add_argument("--go-output", default=str(GO_OUTPUT_PATH))
    args = parser.parse_args()

    write_outputs(
        source_catalog_path=Path(args.source_catalog),
        sdk_json_path=Path(args.sdk_json),
        sdk_markdown_path=Path(args.sdk_markdown),
        focus_json_path=Path(args.focus_json),
        focus_markdown_path=Path(args.focus_markdown),
        go_output_path=Path(args.go_output),
    )
    print(
        json.dumps(
            {
                "source_catalog": args.source_catalog,
                "sdk_json": args.sdk_json,
                "focus_json": args.focus_json,
                "go_output": args.go_output,
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
