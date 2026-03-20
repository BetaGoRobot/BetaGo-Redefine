# AkShare Generated Docs

- `generated/akshare_api_catalog.md`: human-readable Markdown catalog.
- `generated/akshare_api_catalog.json`: structured extraction of the official docs.
- `generated/akshare_openapi.json`: OpenAPI 3.1 skeleton using AKTools' `/api/public/{接口名}` convention.

## Notes

- Sources: `https://akshare.akfamily.xyz/` and `https://aktools.akfamily.xyz/aktools/`.
- Live server: `http://192.168.31.74:1828`.
- Live service reachable: `True`; Swagger docs reachable: `True`; OpenAPI reachable: `True`.
- Live validation for uncertain endpoints: attempted `0`, succeeded `0`, failed `0`.
- The live service currently reports AKShare 1.18.27 while the official docs expose AKShare 1.18.40 content, so newer interfaces may be documentation-only until the service catches up.
- Re-run with `python3 script/akshare_docgen.py` to refresh the source catalog and OpenAPI skeleton.
- Re-run with `python3 script/akshare_focus_codegen.py` to refresh the SDK/focus catalogs and Go client catalog.
