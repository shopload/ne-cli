#!/usr/bin/env python3
"""Generate internal/registry/meta.json from the Next Engine OpenAPI spec.

Usage:
    python3 scripts/gen_meta.py [--openapi openapi.yaml] [--docs llms-full.txt]

Without arguments the spec and the full docs are downloaded from
https://developer.next-engine.com/. Requires PyYAML.
"""
import argparse
import json
import os
import re
import sys
import urllib.request

BASE = "https://developer.next-engine.com"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUT = os.path.join(ROOT, "internal", "registry", "meta.json")

# Endpoints that do not follow /api_v1_<service>_<resource>/<method>.
OVERRIDES = {
    "/api_app/company": ("app", "company", "list"),
    "/api_neauth": ("neauth", "access_token", "get"),
    "/users/sign_in": ("neauth", "uid_state", "get"),
}
TOKEN_PARAMS = {"access_token", "refresh_token"}
READ_METHODS = {"search", "count", "info", "list", "get", "checkconnect",
                "mailaddress", "bundle_candidate_groups"}
# Hard to undo: the CLI asks for --yes before running these.
HIGH_RISK = {"shipped", "bundle", "divide", "bulkupdate", "bulkupdatereceipted"}
TYPES = {"文字列型": "string", "数値型": "number", "日時型": "datetime"}


def load(path, url):
    if path:
        with open(path, encoding="utf-8-sig") as f:
            return f.read()
    with urllib.request.urlopen(url, timeout=120) as r:
        return r.read().decode("utf-8-sig")


def clean(s, limit=400):
    s = re.sub(r"\s+", " ", (s or "")).strip()
    return s if len(s) <= limit else s[: limit - 1] + "…"


class Spec:
    def __init__(self, doc):
        self.doc = doc

    def deref(self, node):
        while isinstance(node, dict) and "$ref" in node:
            cur = self.doc
            for part in node["$ref"].lstrip("#/").split("/"):
                cur = cur[part]
            node = cur
        return node

    def flatten(self, schema):
        """Merge allOf/$ref into (properties, required)."""
        schema = self.deref(schema) or {}
        props, req = {}, []
        for sub in schema.get("allOf", []):
            p, r = self.flatten(sub)
            props.update(p)
            req += r
        props.update(schema.get("properties", {}))
        req += schema.get("required", [])
        return props, req


def field_type(desc):
    for jp, t in TYPES.items():
        if jp in desc:
            return t
    return ""


def name_parts(path):
    if path in OVERRIDES:
        return OVERRIDES[path]
    m = re.fullmatch(r"/api_v1_([a-z]+)_([a-z_]+)/([a-z_]+)", path)
    if not m:
        raise SystemExit(f"cannot map path to a command: {path}")
    return m.group(1), m.group(2), m.group(3)


def build(doc):
    spec = Spec(doc)
    default_host = doc["servers"][0]["url"]
    endpoints = []
    for path, item in doc["paths"].items():
        op = item["post"]
        service, resource, method = name_parts(path)
        body = op["requestBody"]["content"]["application/x-www-form-urlencoded"]["schema"]
        props, required = spec.flatten(body)
        filterable = "SearchFilterMixin" in json.dumps(body)

        params, data_fields = [], []
        for name, p in props.items():
            if name in TOKEN_PARAMS:
                continue
            desc = p.get("description", "")
            desc = desc.split(" / 指定できる項目名")[0]
            params.append({"name": name, "desc": clean(desc),
                           "required": name in required})
            if "contentSchema" in p:
                cs = spec.deref(p["contentSchema"])
                for fname, f in cs.get("properties", {}).items():
                    data_fields.append({
                        "name": fname,
                        "desc": clean(f.get("description", ""), 300),
                        "section": f.get("x-xml-section", ""),
                    })

        resp = op["responses"]["200"]["content"]["application/json"]
        rprops, _ = spec.flatten(resp["schema"])
        fields, response = [], []
        for name, p in rprops.items():
            if name in TOKEN_PARAMS or name.endswith("_token_end_date"):
                continue
            if name == "data" and "items" in p:
                for fname, f in spec.deref(p["items"]).get("properties", {}).items():
                    d = f.get("description", "")
                    fields.append({"name": fname, "desc": clean(d, 200),
                                   "type": field_type(d),
                                   "table": f.get("x-table", "")})
            if name != "result":
                response.append({"name": name, "desc": clean(p.get("description", ""), 200)})

        if method in READ_METHODS:
            risk = "read"
        elif method in HIGH_RISK:
            risk = "high-risk-write"
        else:
            risk = "write"
        ep = {
            "id": f"{service}.{resource}.{method}",
            "path": path,
            "service": service,
            "resource": resource,
            "method": method,
            "tag": op["tags"][0],
            "summary": op["summary"],
            "doc_url": f"{BASE}/api{path}/",
            "auth": "access_token" in props,
            "filterable": filterable,
            "risk": risk,
            "params": params,
        }
        host = (op.get("servers") or [{}])[0].get("url")
        if host and host != default_host:
            ep["host"] = host
        if fields:
            ep["fields"] = fields
        if data_fields:
            ep["data_fields"] = data_fields
        if response:
            ep["response"] = response
        endpoints.append(ep)
    ids = [e["id"] for e in endpoints]
    assert len(ids) == len(set(ids)), "duplicate command ids"
    return {"api_host": default_host, "endpoints": endpoints}


def parse_messages(text):
    """Extract the message-code table (code, message, retryable, note)."""
    m = re.search(r"^# メッセージコード\n(.*?)(?=^# )", text, re.S | re.M)
    out = []
    if not m:
        return out
    for line in m.group(1).splitlines():
        if not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        idx = next((i for i, c in enumerate(cells[:2]) if re.fullmatch(r"\d{6}", c)), None)
        if idx is None:
            continue
        rest = cells[idx + 1:] + ["", "", ""]
        retry = {"○": "yes", "×": "no"}.get(rest[1], "")
        out.append({"code": cells[idx], "message": clean(rest[0], 200),
                    "retry": retry, "note": clean(rest[2], 300)})
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--openapi")
    ap.add_argument("--docs")
    args = ap.parse_args()
    try:
        import yaml
    except ImportError:
        sys.exit("PyYAML is required: pip install pyyaml")
    doc = yaml.safe_load(load(args.openapi, f"{BASE}/openapi.yaml"))
    meta = build(doc)
    meta["messages"] = parse_messages(load(args.docs, f"{BASE}/llms-full.txt"))
    meta["source"] = f"{BASE}/openapi.yaml"
    with open(OUT, "w", encoding="utf-8") as f:
        json.dump(meta, f, ensure_ascii=False, separators=(",", ":"))
        f.write("\n")
    print(f"wrote {OUT}: {len(meta['endpoints'])} endpoints, "
          f"{len(meta['messages'])} message codes", file=sys.stderr)


if __name__ == "__main__":
    main()
