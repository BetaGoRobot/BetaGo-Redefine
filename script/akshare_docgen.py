from __future__ import annotations

import argparse
import json
import posixpath
import re
from collections import Counter
from datetime import datetime, timezone
from html import unescape
from pathlib import Path
from typing import Any
from urllib.parse import quote, urljoin
from urllib.request import Request, urlopen

BASE_DOC_URL = "https://akshare.akfamily.xyz/"
AKTOOLS_DOC_URL = "https://aktools.akfamily.xyz/aktools/"
DEFAULT_SERVER_URL = "http://192.168.31.74:1828"
USER_AGENT = "Mozilla/5.0 (compatible; akshare-docgen/1.0)"
DEFAULT_PAGES = ["indicator", "trade"]
TIMEOUT_SECONDS = 60

TABLE_SPLIT_RE = re.compile(r"\s*\|\s*")
HEADING_RE = re.compile(r"^(#{1,6})\s+(.+?)\s*$")
TITLE_RE = re.compile(r"<title>(.*?)</title>", re.IGNORECASE | re.DOTALL)
SOURCE_LINK_RE = re.compile(r'href="([^"]*?_sources/[^"]+?\.txt)"')
TOCTREE_ENTRY_RE = re.compile(r"^\s{4}([A-Za-z0-9_./-]+)\s*$")
CURRENT_VERSION_RE = re.compile(r"当前的 AKTools 版本为：([^，<]+)，AKShare 版本为：([^<]+)")
LATEST_VERSION_RE = re.compile(r"最新的 AKTools 版本为：([^，<]+)，AKShare 版本为：([^<]+)")


def fetch_text(url: str) -> str:
    request = Request(url, headers={"User-Agent": USER_AGENT})
    with urlopen(request, timeout=TIMEOUT_SECONDS) as response:
        return response.read().decode("utf-8", errors="replace")


def fetch_json(url: str) -> Any:
    return json.loads(fetch_text(url))


def strip_markdown(text: str) -> str:
    text = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", text)
    text = text.replace("`", "")
    return " ".join(unescape(text).strip().split())


def extract_page_title(html: str) -> str:
    match = TITLE_RE.search(html)
    if not match:
        return ""
    title = unescape(match.group(1))
    return strip_markdown(title.split("&mdash;")[0])


def extract_source_url(page_url: str, html: str) -> str:
    match = SOURCE_LINK_RE.search(html)
    if match:
        return urljoin(page_url, unescape(match.group(1)))
    if page_url.endswith(".html"):
        base = page_url[: -len(".html")]
        return f"{base}.md.txt"
    return page_url


def split_markdown_row(line: str) -> list[str]:
    inner = line.strip().strip("|")
    if not inner:
        return []
    return [cell.strip() for cell in TABLE_SPLIT_RE.split(inner)]


def is_markdown_table_divider(line: str) -> bool:
    cells = split_markdown_row(line)
    return bool(cells) and all(re.fullmatch(r":?-{3,}:?", cell) for cell in cells)


def parse_markdown_table(lines: list[str], start_index: int) -> tuple[list[dict[str, str]], int]:
    header_cells = split_markdown_row(lines[start_index])
    index = start_index + 1
    if index < len(lines) and is_markdown_table_divider(lines[index]):
        index += 1

    rows: list[dict[str, str]] = []
    while index < len(lines):
        line = lines[index].rstrip()
        if not line.strip().startswith("|"):
            break
        cells = split_markdown_row(line)
        if len(cells) < len(header_cells):
            cells.extend([""] * (len(header_cells) - len(cells)))
        row = {
            strip_markdown(header_cells[pos]): strip_markdown(cells[pos]) if pos < len(cells) else ""
            for pos in range(len(header_cells))
        }
        rows.append(row)
        index += 1
    return rows, index


def normalize_table_rows(rows: list[dict[str, str]]) -> list[dict[str, str]]:
    normalized: list[dict[str, str]] = []
    for row in rows:
        description = row.get("描述", row.get("Description", row.get("说明", ""))).strip()
        example = row.get("举例", "").strip()
        if example:
            description = f"{description} Example: {example}".strip()
        normalized_row = {
            "name": row.get("名称", row.get("Name", row.get("参数名", ""))).strip(),
            "type": row.get("类型", row.get("Type", "")).strip(),
            "description": description,
        }
        if any(normalized_row.values()):
            normalized.append(normalized_row)
    return normalized


def truncate_block(text: str, max_lines: int = 20) -> str:
    lines = text.strip().splitlines()
    if len(lines) <= max_lines:
        return "\n".join(lines)
    return "\n".join(lines[:max_lines] + ["...", f"(truncated, total {len(lines)} lines)"])


def schema_for_dtype(raw_type: str, *, for_response: bool = False) -> dict[str, Any]:
    value = raw_type.strip().lower()
    if value in {"str", "string"}:
        return {"type": "string"}
    if value in {"int", "int32", "int64", "integer"}:
        return {"type": "integer"}
    if value in {"float", "float32", "float64", "double", "number"}:
        return {"type": "number"}
    if value in {"bool", "boolean"}:
        return {"type": "boolean"}
    if value in {"list", "array"}:
        return {"type": "array", "items": {}}
    if value in {"dict", "map"}:
        return {"type": "object", "additionalProperties": True}
    if value in {"date"}:
        return {"type": "string", "format": "date"}
    if value in {"datetime", "timestamp"}:
        return {"type": "string", "format": "date-time"}
    if value == "object":
        description = "AKShare docs mark this as pandas object dtype; actual JSON values may be string, number, boolean, null, or mixed."
        if for_response:
            return {"description": description}
        return {"type": "string", "description": description}
    if not value:
        return {}
    return {"description": f"Unmapped AKShare type: {raw_type}"}


def merge_output_fields(output_groups: list[dict[str, Any]]) -> list[dict[str, str]]:
    merged: list[dict[str, str]] = []
    seen: set[str] = set()
    for group in output_groups:
        for field in group.get("fields", []):
            name = field.get("name", "")
            if not name or name in seen or name == "接口示例":
                continue
            seen.add(name)
            merged.append(field)
    return merged


def preprocess_source_text(source_text: str, category_slug: str) -> str:
    if category_slug != "qhkc":
        return source_text

    lines = source_text.splitlines()
    normalized: list[str] = []
    index = 0

    while index < len(lines):
        line = lines[index].rstrip()
        stripped = line.strip()

        if re.fullmatch(r"#{3,4}\s+接口名称", stripped):
            index += 1
            while index < len(lines) and not lines[index].strip():
                index += 1
            if index < len(lines):
                normalized.append(f"接口: {strip_markdown(lines[index])}")
                index += 1
            continue

        if re.fullmatch(r"#{3,4}\s+接口描述", stripped):
            index += 1
            while index < len(lines) and not lines[index].strip():
                index += 1
            if index < len(lines):
                normalized.append(f"描述: {strip_markdown(lines[index])}")
                index += 1
            continue

        if re.fullmatch(r"#{3,4}\s+请求参数", stripped):
            normalized.append("输入参数")
            index += 1
            continue

        if re.fullmatch(r"#{3,4}\s+返回参数", stripped):
            normalized.append("输出参数")
            index += 1
            continue

        if re.fullmatch(r"#{3,4}\s+示例代码", stripped):
            normalized.append("接口示例")
            index += 1
            continue

        if re.fullmatch(r"#{3,4}\s+返回示例", stripped):
            normalized.append("数据示例")
            index += 1
            continue

        normalized.append(line)
        index += 1

    return "\n".join(normalized)


def parse_source_text(
    source_text: str,
    *,
    page_title: str,
    category_slug: str,
    page_url: str,
    source_url: str,
) -> list[dict[str, Any]]:
    lines = source_text.splitlines()
    headings: list[str] = []
    records: list[dict[str, Any]] = []
    index = 0
    base_heading_level: int | None = None

    while index < len(lines):
        raw_line = lines[index].rstrip()
        heading_match = HEADING_RE.match(raw_line)
        if heading_match:
            level = len(heading_match.group(1))
            title = strip_markdown(heading_match.group(2))
            if not title:
                index += 1
                continue
            if base_heading_level is None or level < base_heading_level:
                base_heading_level = level
            effective_level = max(1, level - base_heading_level + 1)
            while len(headings) >= effective_level:
                headings.pop()
            headings.append(title)
            index += 1
            continue

        if raw_line.startswith("接口:"):
            current_headings = headings.copy()
            if not current_headings or current_headings[0] != page_title:
                current_headings = [page_title] + [item for item in current_headings if item != page_title]

            record: dict[str, Any] = {
                "interface": strip_markdown(raw_line.split("接口:", 1)[1]),
                "page_title": page_title,
                "category_slug": category_slug,
                "page_url": page_url,
                "source_url": source_url,
                "headings": current_headings,
                "target_url": "",
                "description": "",
                "limit": "",
                "input_params": [],
                "output_groups": [],
                "example_code": "",
                "sample_data": "",
                "notes": [],
            }
            current_section = ""
            pending_output_notes: list[str] = []
            index += 1

            while index < len(lines):
                line = lines[index].rstrip()
                if line.startswith("接口:") or HEADING_RE.match(line):
                    break
                stripped = line.strip()
                if not stripped:
                    index += 1
                    continue
                if stripped.startswith("目标地址:"):
                    record["target_url"] = strip_markdown(stripped.split("目标地址:", 1)[1])
                    index += 1
                    continue
                if stripped.startswith("描述:"):
                    record["description"] = strip_markdown(stripped.split("描述:", 1)[1])
                    index += 1
                    continue
                if stripped.startswith("限量:"):
                    record["limit"] = strip_markdown(stripped.split("限量:", 1)[1])
                    index += 1
                    continue
                if stripped.startswith("输入参数"):
                    current_section = stripped
                    pending_output_notes = []
                    index += 1
                    continue
                if stripped.startswith("输出参数"):
                    current_section = stripped
                    pending_output_notes = []
                    index += 1
                    continue
                if stripped == "接口示例":
                    current_section = stripped
                    index += 1
                    continue
                if stripped == "数据示例":
                    current_section = stripped
                    index += 1
                    continue
                if stripped.startswith("|"):
                    rows, next_index = parse_markdown_table(lines, index)
                    normalized_rows = normalize_table_rows(rows)
                    if current_section.startswith("输入参数"):
                        record["input_params"] = normalized_rows
                    elif current_section.startswith("输出参数"):
                        record["output_groups"].append(
                            {
                                "section": current_section,
                                "notes": pending_output_notes.copy(),
                                "fields": normalized_rows,
                            }
                        )
                        pending_output_notes.clear()
                    index = next_index
                    continue
                if stripped.startswith("```"):
                    fence = stripped[:3]
                    lang = stripped[3:].strip()
                    block_lines: list[str] = []
                    index += 1
                    while index < len(lines) and not lines[index].strip().startswith(fence):
                        block_lines.append(lines[index].rstrip("\n"))
                        index += 1
                    block = "\n".join(block_lines).strip()
                    if current_section == "接口示例" and block:
                        record["example_code"] = block if not lang else block
                    elif current_section == "数据示例" and block:
                        record["sample_data"] = block
                    else:
                        record["notes"].append(block)
                    if index < len(lines) and lines[index].strip().startswith(fence):
                        index += 1
                    continue

                if current_section.startswith("输出参数"):
                    pending_output_notes.append(strip_markdown(stripped))
                else:
                    record["notes"].append(strip_markdown(stripped))
                index += 1

            records.append(record)
            continue

        index += 1

    return records


def discover_data_pages() -> list[str]:
    data_index_url = urljoin(BASE_DOC_URL, "_sources/data/index.rst.txt")
    data_index_text = fetch_text(data_index_url)
    return extract_toctree_entries(data_index_text, "data/index")


def extract_toctree_entries(source_text: str, current_doc_path: str) -> list[str]:
    entries: list[str] = []
    for line in source_text.splitlines():
        match = TOCTREE_ENTRY_RE.match(line.rstrip())
        if not match:
            continue
        entry = match.group(1)
        if entry.endswith(".md"):
            entry = entry[: -len(".md")]
        if entry.endswith(".rst"):
            entry = entry[: -len(".rst")]
        resolved = posixpath.normpath(posixpath.join(posixpath.dirname(current_doc_path), entry))
        entries.append(resolved)
    return entries


def load_page(doc_path: str) -> dict[str, Any]:
    page_url = urljoin(BASE_DOC_URL, f"{doc_path}.html")
    page_html = fetch_text(page_url)
    page_title = extract_page_title(page_html) or doc_path.split("/")[-1]
    source_url = extract_source_url(page_url, page_html)
    source_text = fetch_text(source_url)
    return {
        "doc_path": doc_path,
        "page_url": page_url,
        "page_title": page_title,
        "source_url": source_url,
        "source_text": source_text,
    }


def discover_pages(seed_paths: list[str]) -> tuple[list[str], dict[str, dict[str, Any]], list[str]]:
    queue = list(seed_paths)
    seen: set[str] = set()
    ordered: list[str] = []
    loaded_pages: dict[str, dict[str, Any]] = {}
    failures: list[str] = []

    while queue:
        doc_path = queue.pop(0)
        if doc_path in seen:
            continue
        seen.add(doc_path)
        try:
            page = load_page(doc_path)
            loaded_pages[doc_path] = page
            ordered.append(doc_path)
            for entry in extract_toctree_entries(page["source_text"], doc_path):
                if entry not in seen:
                    queue.append(entry)
        except Exception as exc:  # noqa: BLE001
            failures.append(f"{doc_path}: {exc}")

    return ordered, loaded_pages, failures


def page_records_from_loaded_page(page: dict[str, Any]) -> list[dict[str, Any]]:
    doc_path = page["doc_path"]
    category_slug = doc_path.split("/")[1] if doc_path.startswith("data/") else doc_path.split("/")[-1]
    return parse_source_text(
        preprocess_source_text(page["source_text"], category_slug),
        page_title=page["page_title"],
        category_slug=category_slug,
        page_url=page["page_url"],
        source_url=page["source_url"],
    )


def fetch_server_metadata(server_url: str) -> dict[str, Any]:
    metadata: dict[str, Any] = {
        "server_url": server_url,
        "reachable": False,
        "docs_reachable": False,
        "openapi_reachable": False,
        "current_versions": {},
        "latest_versions": {},
    }
    try:
        homepage = fetch_text(f"{server_url.rstrip('/')}/")
        metadata["reachable"] = True
        current_match = CURRENT_VERSION_RE.search(homepage)
        latest_match = LATEST_VERSION_RE.search(homepage)
        if current_match:
            metadata["current_versions"] = {
                "aktools": current_match.group(1).strip(),
                "akshare": current_match.group(2).strip(),
            }
        if latest_match:
            metadata["latest_versions"] = {
                "aktools": latest_match.group(1).strip(),
                "akshare": latest_match.group(2).strip(),
            }
    except Exception as exc:  # noqa: BLE001
        metadata["homepage_error"] = str(exc)

    try:
        docs_html = fetch_text(f"{server_url.rstrip('/')}/docs")
        metadata["docs_reachable"] = "Swagger UI" in docs_html
    except Exception as exc:  # noqa: BLE001
        metadata["docs_error"] = str(exc)

    try:
        openapi = fetch_json(f"{server_url.rstrip('/')}/openapi.json")
        metadata["openapi_reachable"] = True
        metadata["openapi_info"] = openapi.get("info", {})
        metadata["openapi_paths"] = sorted(openapi.get("paths", {}).keys())
    except Exception as exc:  # noqa: BLE001
        metadata["openapi_error"] = str(exc)

    return metadata


def validate_uncertain_records(
    records: list[dict[str, Any]],
    server_url: str,
    *,
    limit: int,
) -> dict[str, Any]:
    summary: dict[str, Any] = {
        "attempted": 0,
        "succeeded": 0,
        "failed": 0,
        "details": [],
    }
    candidates = [record for record in records if not merge_output_fields(record["output_groups"])]

    for record in candidates[:limit]:
        summary["attempted"] += 1
        url = f"{server_url.rstrip('/')}/api/public/{quote(record['interface'])}"
        detail: dict[str, Any] = {"interface": record["interface"], "url": url}
        try:
            payload = fetch_json(url)
            detail["status"] = "ok"
            detail["payload_type"] = type(payload).__name__
            if isinstance(payload, list):
                detail["rows"] = len(payload)
                if payload and isinstance(payload[0], dict):
                    keys = list(payload[0].keys())
                    detail["sample_keys"] = keys
                    record["output_groups"].append(
                        {
                            "section": "输出参数-实测推断",
                            "notes": [
                                "Inferred from live AKTools response because the official docs did not expose a structured output table.",
                            ],
                            "fields": [
                                {
                                    "name": key,
                                    "type": "",
                                    "description": "Inferred from live response keys.",
                                }
                                for key in keys
                            ],
                        }
                    )
            elif isinstance(payload, dict):
                detail["keys"] = list(payload.keys())
            record["validation"] = detail
            summary["succeeded"] += 1
        except Exception as exc:  # noqa: BLE001
            detail["status"] = "error"
            detail["error"] = str(exc)
            record["validation"] = detail
            summary["failed"] += 1
        summary["details"].append(detail)

    return summary


def build_markdown(
    records: list[dict[str, Any]],
    failures: list[str],
    server_url: str,
    server_metadata: dict[str, Any],
    validation_summary: dict[str, Any],
) -> str:
    generated_at = datetime.now(timezone.utc).isoformat()
    category_counts = Counter(record["category_slug"] for record in records)
    page_counts = Counter(record["page_title"] for record in records)
    lines = [
        "# AkShare API Catalog",
        "",
        f"- Generated at: `{generated_at}`",
        f"- Total APIs: `{len(records)}`",
        f"- Total categories: `{len(category_counts)}`",
        f"- Primary docs: <https://akshare.akfamily.xyz/>",
        f"- HTTP path convention reference: <{AKTOOLS_DOC_URL}>",
        f"- Server base URL: `{server_url}`",
        f"- Server reachable: `{server_metadata.get('reachable', False)}`",
        f"- Swagger docs reachable: `{server_metadata.get('docs_reachable', False)}`",
        f"- OpenAPI reachable: `{server_metadata.get('openapi_reachable', False)}`",
    ]
    current_versions = server_metadata.get("current_versions", {})
    latest_versions = server_metadata.get("latest_versions", {})
    if current_versions:
        lines.append(
            f"- Live service versions: AKTools `{current_versions.get('aktools', '')}`, AKShare `{current_versions.get('akshare', '')}`"
        )
    if latest_versions:
        lines.append(
            f"- Service homepage reports latest versions: AKTools `{latest_versions.get('aktools', '')}`, AKShare `{latest_versions.get('akshare', '')}`"
        )
    if validation_summary.get("attempted"):
        lines.append(
            f"- Live validation for uncertain endpoints: attempted `{validation_summary['attempted']}`, succeeded `{validation_summary['succeeded']}`, failed `{validation_summary['failed']}`"
        )
    lines.extend(["", "## Category Summary", "", "| Category | API Count | Page Count |", "|---|---:|---:|"])
    for category, count in sorted(category_counts.items()):
        related_page_count = sum(1 for page in page_counts if category in page.lower() or category == page)
        lines.append(f"| {category} | {count} | {related_page_count} |")

    if failures:
        lines.extend(
            [
                "",
                "## Fetch Failures",
                "",
            ]
        )
        for failure in failures:
            lines.append(f"- {failure}")

    lines.extend(
        [
            "",
            "## API Index",
            "",
            "| API | Category | Deepest Heading | Doc Page |",
            "|---|---|---|---|",
        ]
    )
    for record in records:
        deepest_heading = record["headings"][-1] if record["headings"] else record["page_title"]
        lines.append(
            f"| `{record['interface']}` | {record['category_slug']} | {deepest_heading} | {record['page_url']} |"
        )

    lines.extend(["", "## Detailed Specs", ""])
    for record in records:
        lines.extend(
            [
                f"### `{record['interface']}`",
                "",
                f"- Category: `{record['category_slug']}`",
                f"- Page: {record['page_url']}",
                f"- Source: {record['source_url']}",
                f"- HTTP Path: `GET /api/public/{record['interface']}`",
                f"- Heading Path: {' / '.join(record['headings'])}",
            ]
        )
        if record["description"]:
            lines.append(f"- Description: {record['description']}")
        if record["target_url"]:
            lines.append(f"- Target URL: {record['target_url']}")
        if record["limit"]:
            lines.append(f"- Limit: {record['limit']}")
        if record["notes"]:
            note_text = "; ".join(note for note in record["notes"] if note)
            if note_text:
                lines.append(f"- Notes: {note_text}")
        if record.get("validation"):
            validation = record["validation"]
            lines.append(f"- Validation Status: {validation.get('status', '')}")
            if validation.get("sample_keys"):
                lines.append(f"- Validation Keys: {', '.join(validation['sample_keys'])}")
            if validation.get("error"):
                lines.append(f"- Validation Error: {validation['error']}")

        lines.extend(["", "#### Input Params", ""])
        if record["input_params"]:
            lines.extend(["| Name | Type | Description |", "|---|---|---|"])
            for item in record["input_params"]:
                lines.append(f"| {item['name']} | {item['type']} | {item['description']} |")
        else:
            lines.append("_None documented_")

        lines.extend(["", "#### Output Fields", ""])
        if record["output_groups"]:
            for output_group in record["output_groups"]:
                lines.append(f"##### {output_group['section']}")
                lines.append("")
                if output_group["notes"]:
                    lines.append(f"Notes: {'; '.join(output_group['notes'])}")
                    lines.append("")
                lines.extend(["| Name | Type | Description |", "|---|---|---|"])
                for field in output_group["fields"]:
                    lines.append(f"| {field['name']} | {field['type']} | {field['description']} |")
                lines.append("")
        else:
            lines.append("_No structured output table found_")
            lines.append("")

        if record["example_code"]:
            lines.extend(["#### Example", "", "```python", record["example_code"], "```", ""])
        if record["sample_data"]:
            lines.extend(["#### Sample Data", "", "```text", truncate_block(record["sample_data"]), "```", ""])

    return "\n".join(lines).strip() + "\n"


def build_openapi(
    records: list[dict[str, Any]],
    server_url: str,
    server_metadata: dict[str, Any],
    validation_summary: dict[str, Any],
) -> dict[str, Any]:
    generated_at = datetime.now(timezone.utc).isoformat()
    paths: dict[str, Any] = {}

    for record in records:
        parameters = []
        for item in record["input_params"]:
            name = item["name"].strip()
            if not name or name == "-":
                continue
            parameter = {
                "name": name,
                "in": "query",
                "required": False,
                "schema": schema_for_dtype(item["type"]),
                "description": item["description"],
            }
            parameters.append(parameter)

        response_fields = merge_output_fields(record["output_groups"])
        properties: dict[str, Any] = {}
        for field in response_fields:
            schema = schema_for_dtype(field["type"], for_response=True)
            if field["description"]:
                schema["description"] = field["description"] if "description" not in schema else f"{schema['description']} {field['description']}".strip()
            properties[field["name"]] = schema or {}

        summary = record["headings"][-1] if record["headings"] else record["page_title"]
        description_parts = [
            f"Doc page: {record['page_url']}",
            f"Source page: {record['source_url']}",
            f"Heading path: {' / '.join(record['headings'])}",
        ]
        if record["description"]:
            description_parts.append(f"Description: {record['description']}")
        if record["target_url"]:
            description_parts.append(f"Target URL: {record['target_url']}")
        if record["limit"]:
            description_parts.append(f"Limit: {record['limit']}")
        if record["notes"]:
            description_parts.append(f"Notes: {'; '.join(record['notes'])}")

        paths[f"/api/public/{record['interface']}"] = {
            "get": {
                "operationId": record["interface"],
                "summary": summary,
                "description": "\n".join(description_parts),
                "tags": [record["category_slug"]],
                "parameters": parameters,
                "responses": {
                    "200": {
                        "description": "AKTools typically serializes AKShare DataFrame output as a JSON array of row objects.",
                        "content": {
                            "application/json": {
                                "schema": {
                                    "type": "array",
                                    "items": {
                                        "type": "object",
                                        "properties": properties,
                                        "additionalProperties": True,
                                    },
                                }
                            }
                        },
                    }
                },
                "x-akshare": {
                    "page_title": record["page_title"],
                    "page_url": record["page_url"],
                    "source_url": record["source_url"],
                    "target_url": record["target_url"],
                    "limit": record["limit"],
                    "output_sections": [group["section"] for group in record["output_groups"]],
                    "generated_at": generated_at,
                },
            }
        }

    return {
        "openapi": "3.1.0",
        "info": {
            "title": "AkShare HTTP API (Generated)",
            "version": generated_at.split("T")[0],
            "description": (
                "Generated from official AkShare documentation and AKTools HTTP usage docs. "
                "The path convention follows `/api/public/{akshare_function}`. "
                f"Live service reachability: reachable={server_metadata.get('reachable', False)}, "
                f"openapi={server_metadata.get('openapi_reachable', False)}. "
                f"Uncertain endpoints validated: attempted={validation_summary.get('attempted', 0)}, "
                f"succeeded={validation_summary.get('succeeded', 0)}, failed={validation_summary.get('failed', 0)}."
            ),
        },
        "servers": [{"url": server_url}],
        "paths": paths,
    }


def write_outputs(
    *,
    records: list[dict[str, Any]],
    failures: list[str],
    output_dir: Path,
    server_url: str,
    server_metadata: dict[str, Any],
    validation_summary: dict[str, Any],
) -> None:
    output_dir.mkdir(parents=True, exist_ok=True)

    catalog_json_path = output_dir / "akshare_api_catalog.json"
    catalog_md_path = output_dir / "akshare_api_catalog.md"
    openapi_json_path = output_dir / "akshare_openapi.json"
    readme_path = output_dir.parent / "README.md"

    catalog_json = {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "primary_docs": BASE_DOC_URL,
        "aktools_docs": AKTOOLS_DOC_URL,
        "server_url": server_url,
        "server_metadata": server_metadata,
        "validation": validation_summary,
        "failures": failures,
        "records": records,
    }

    catalog_json_path.write_text(
        json.dumps(catalog_json, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    catalog_md_path.write_text(
        build_markdown(records, failures, server_url, server_metadata, validation_summary),
        encoding="utf-8",
    )
    openapi_json_path.write_text(
        json.dumps(build_openapi(records, server_url, server_metadata, validation_summary), ensure_ascii=False, indent=2)
        + "\n",
        encoding="utf-8",
    )

    readme_lines = [
        "# AkShare Generated Docs",
        "",
        "- `generated/akshare_api_catalog.md`: human-readable Markdown catalog.",
        "- `generated/akshare_api_catalog.json`: structured extraction of the official docs.",
        "- `generated/akshare_openapi.json`: OpenAPI 3.1 skeleton using AKTools' `/api/public/{接口名}` convention.",
        "",
        "## Notes",
        "",
        "- Sources: `https://akshare.akfamily.xyz/` and `https://aktools.akfamily.xyz/aktools/`.",
        f"- Live server: `{server_url}`.",
        f"- Live service reachable: `{server_metadata.get('reachable', False)}`; Swagger docs reachable: `{server_metadata.get('docs_reachable', False)}`; OpenAPI reachable: `{server_metadata.get('openapi_reachable', False)}`.",
        f"- Live validation for uncertain endpoints: attempted `{validation_summary.get('attempted', 0)}`, succeeded `{validation_summary.get('succeeded', 0)}`, failed `{validation_summary.get('failed', 0)}`.",
        "- The live service currently reports AKShare 1.18.27 while the official docs expose AKShare 1.18.40 content, so newer interfaces may be documentation-only until the service catches up.",
        "- Re-run with `python3 script/akshare_docgen.py` to refresh the generated files.",
        "",
    ]
    readme_path.write_text("\n".join(readme_lines), encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description="Generate AkShare API docs from official sources.")
    parser.add_argument(
        "--output-dir",
        default="docs/akshare/generated",
        help="Directory for generated files.",
    )
    parser.add_argument(
        "--server-url",
        default=DEFAULT_SERVER_URL,
        help="Base server URL to place into the OpenAPI output.",
    )
    parser.add_argument(
        "--validate-limit",
        type=int,
        default=30,
        help="How many uncertain endpoints to validate live against the AKTools server.",
    )
    args = parser.parse_args()

    seed_doc_paths = discover_data_pages() + DEFAULT_PAGES
    discovered_doc_paths, loaded_pages, failures = discover_pages(seed_doc_paths)
    records: list[dict[str, Any]] = []
    for doc_path in discovered_doc_paths:
        records.extend(page_records_from_loaded_page(loaded_pages[doc_path]))

    records.sort(key=lambda item: (item["category_slug"], item["interface"]))
    server_metadata = fetch_server_metadata(args.server_url)
    validation_summary = validate_uncertain_records(
        records,
        args.server_url,
        limit=max(args.validate_limit, 0),
    )
    write_outputs(
        records=records,
        failures=failures,
        output_dir=Path(args.output_dir),
        server_url=args.server_url,
        server_metadata=server_metadata,
        validation_summary=validation_summary,
    )
    print(
        json.dumps(
            {
                "doc_pages": len(discovered_doc_paths),
                "records": len(records),
                "failures": len(failures),
                "validated": validation_summary["attempted"],
                "output_dir": args.output_dir,
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
