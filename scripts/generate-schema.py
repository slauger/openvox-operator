#!/usr/bin/env python3
"""Generate values.schema.json from values.yaml with helm-docs (# --) and @schema annotations."""

import sys
import json
import re
import yaml


def parse_type(value):
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return "string"


def parse_schema_annotation(annotation_str):
    """Parse '# @schema key:value;key:value' annotations."""
    result = {}
    for part in annotation_str.split(";"):
        part = part.strip()
        if ":" not in part:
            continue
        key, _, val = part.partition(":")
        key = key.strip()
        val = val.strip()
        if key == "minimum":
            result[key] = int(val)
        elif key == "maximum":
            result[key] = int(val)
        elif key == "required":
            result[key] = val.lower() == "true"
        elif key == "hidden":
            result[key] = True
        elif key == "type":
            result[key] = val
        elif key == "pattern":
            result[key] = val
        elif key == "enum":
            # format: [val1, val2, val3]
            inner = val.strip("[]")
            result[key] = [v.strip() for v in inner.split(",")]
        else:
            result[key] = val
    return result


def parse_comments(lines, start_idx):
    """Look backwards from start_idx to collect # -- description and # @schema annotations."""
    description = []
    schema_props = {}
    hidden = False
    i = start_idx - 1
    comment_lines = []
    while i >= 0 and lines[i].strip().startswith("#"):
        comment_lines.insert(0, lines[i])
        i -= 1

    for line in comment_lines:
        stripped = line.strip()
        # inline @schema on value line is handled separately
        if stripped.startswith("# @schema"):
            annotation = stripped[len("# @schema"):].strip()
            if annotation == "hidden":
                hidden = True
            else:
                schema_props.update(parse_schema_annotation(annotation))
        elif stripped.startswith("# --"):
            description.append(stripped[4:].strip())

    return " ".join(description) if description else None, schema_props, hidden


def build_schema(data, lines, indent_level=0):
    """Recursively build JSON schema from parsed YAML data and source lines."""
    if not isinstance(data, dict):
        return {}

    properties = {}
    required = []

    # We need to match keys in order to find their line numbers
    # Re-parse lines to find key positions at this indent level
    key_lines = {}
    for idx, line in enumerate(lines):
        stripped = line.strip()
        if stripped.startswith("#") or not stripped:
            continue
        # Match yaml key at any indent
        m = re.match(r'^(\s*)([a-zA-Z_][a-zA-Z0-9_\-]*)\s*:', line)
        if m:
            key = m.group(2)
            key_lines.setdefault(key, []).append(idx)

    used_indices = {}

    for key, value in data.items():
        # Find the first unused line index for this key
        indices = key_lines.get(key, [])
        used = used_indices.get(key, 0)
        if used < len(indices):
            line_idx = indices[used]
            used_indices[key] = used + 1
        else:
            line_idx = 0

        desc, schema_props, hidden = parse_comments(lines, line_idx)

        if hidden:
            continue

        # Check for inline @schema on the value line
        value_line = lines[line_idx] if line_idx < len(lines) else ""
        inline_match = re.search(r'#\s*@schema\s+(.+)$', value_line)
        if inline_match:
            annotation = inline_match.group(1).strip()
            if annotation == "hidden":
                continue
            schema_props.update(parse_schema_annotation(annotation))

        prop = {}
        if desc:
            prop["description"] = desc

        explicit_type = schema_props.pop("type", None)
        is_required = schema_props.pop("required", False)

        if isinstance(value, dict) and value:
            prop["type"] = "object"
            sub = build_schema(value, lines, indent_level + 1)
            if sub.get("properties"):
                prop["properties"] = sub["properties"]
            if sub.get("required"):
                prop["required"] = sub["required"]
        elif isinstance(value, list):
            prop["type"] = "array"
        elif value is None:
            prop["type"] = explicit_type or "string"
        else:
            prop["type"] = explicit_type or parse_type(value)

        # Apply remaining schema annotations
        for k, v in schema_props.items():
            prop[k] = v

        properties[key] = prop
        if is_required:
            required.append(key)

    result = {"type": "object", "properties": dict(sorted(properties.items()))}
    if required:
        result["required"] = sorted(required)
    return result


def main():
    if len(sys.argv) < 2:
        print("Usage: generate-schema.py <values.yaml> [output.json]", file=sys.stderr)
        sys.exit(1)

    input_file = sys.argv[1]
    output_file = sys.argv[2] if len(sys.argv) > 2 else "values.schema.json"

    with open(input_file) as f:
        content = f.read()

    lines = content.splitlines()
    data = yaml.safe_load(content)

    schema = {
        "$schema": "https://json-schema.org/draft/2020-12/schema",
    }
    schema.update(build_schema(data, lines))

    with open(output_file, "w") as f:
        json.dump(schema, f, indent=2)
        f.write("\n")

    print(f"Schema written to {output_file}")


if __name__ == "__main__":
    main()
