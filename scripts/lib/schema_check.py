#!/usr/bin/env python3
"""A deliberately small, fail-closed JSON Schema (2020-12 subset) validator.

Release records are security-relevant, so a schema must be *executed*, not just
declared. This validator supports exactly the keywords the Telos release
schemas use and raises SchemaError for any other keyword. That way a schema
cannot claim a constraint (for example ``format`` or ``dependentRequired``)
that nothing enforces.

Supported: $schema, $id, title, description, $defs, $ref (local ``#/$defs/x``),
type (string or list), const, enum, pattern, minLength, maxLength, minimum,
maximum, minItems, maxItems, uniqueItems, items, properties, required,
additionalProperties (``false`` or a schema), minProperties, oneOf.
"""

import re

ANNOTATIONS = {"$schema", "$id", "title", "description"}
KEYWORDS = ANNOTATIONS | {
    "$defs", "$ref", "type", "const", "enum", "pattern", "minLength",
    "maxLength", "minimum", "maximum", "minItems", "maxItems", "uniqueItems",
    "items", "properties", "required", "additionalProperties",
    "minProperties", "oneOf",
}
TYPES = {"object", "array", "string", "integer", "boolean", "null"}


class SchemaError(Exception):
    """The schema itself uses an unsupported or malformed construct."""


class ValidationError(Exception):
    """An instance violates a schema; ``path`` locates the first violation."""

    def __init__(self, path, message):
        super().__init__(f"{path}: {message}")
        self.path = path
        self.message = message


def _is_integer(value):
    return isinstance(value, int) and not isinstance(value, bool)


def _type_matches(value, name):
    if name == "object":
        return isinstance(value, dict)
    if name == "array":
        return isinstance(value, list)
    if name == "string":
        return isinstance(value, str)
    if name == "integer":
        return _is_integer(value)
    if name == "boolean":
        return isinstance(value, bool)
    if name == "null":
        return value is None
    raise SchemaError(f"unsupported type {name!r}")


def _resolve(root, reference):
    if not isinstance(reference, str) or not reference.startswith("#/$defs/"):
        raise SchemaError(f"unsupported $ref {reference!r}")
    name = reference[len("#/$defs/"):]
    definitions = root.get("$defs")
    if not isinstance(definitions, dict) or name not in definitions:
        raise SchemaError(f"unresolved $ref {reference!r}")
    return definitions[name]


def check_schema(schema, root=None, path="#"):
    """Reject unsupported keywords and malformed keyword values up front."""
    root = schema if root is None else root
    if schema is True or schema is False:
        return
    if not isinstance(schema, dict):
        raise SchemaError(f"{path}: schema must be an object")
    unknown = set(schema) - KEYWORDS
    if unknown:
        raise SchemaError(f"{path}: unsupported keyword {sorted(unknown)[0]!r}")
    if "type" in schema:
        names = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
        if not names or any(name not in TYPES for name in names):
            raise SchemaError(f"{path}: invalid type")
    if "pattern" in schema:
        try:
            re.compile(schema["pattern"])
        except (re.error, TypeError) as error:
            raise SchemaError(f"{path}: invalid pattern: {error}") from error
    if "$ref" in schema:
        _resolve(root, schema["$ref"])
    for key in ("minLength", "maxLength", "minItems", "maxItems", "minProperties"):
        if key in schema and (not _is_integer(schema[key]) or schema[key] < 0):
            raise SchemaError(f"{path}: {key} must be a nonnegative integer")
    for key in ("minimum", "maximum"):
        if key in schema and not _is_integer(schema[key]):
            raise SchemaError(f"{path}: {key} must be an integer")
    if "required" in schema and (
        not isinstance(schema["required"], list)
        or any(not isinstance(item, str) for item in schema["required"])
    ):
        raise SchemaError(f"{path}: required must be a list of strings")
    if "enum" in schema and not isinstance(schema["enum"], list):
        raise SchemaError(f"{path}: enum must be a list")
    if "$defs" in schema:
        for name, child in schema["$defs"].items():
            check_schema(child, root, f"{path}/$defs/{name}")
    if "items" in schema:
        check_schema(schema["items"], root, f"{path}/items")
    for name, child in schema.get("properties", {}).items():
        check_schema(child, root, f"{path}/properties/{name}")
    if "additionalProperties" in schema:
        extra = schema["additionalProperties"]
        if extra is not False and extra is not True:
            check_schema(extra, root, f"{path}/additionalProperties")
    for index, child in enumerate(schema.get("oneOf", [])):
        check_schema(child, root, f"{path}/oneOf/{index}")


def validate(instance, schema, root=None, path="$"):
    """Raise ValidationError for the first violation of ``schema``."""
    root = schema if root is None else root
    if schema is True:
        return
    if schema is False:
        raise ValidationError(path, "no value is permitted here")
    if "$ref" in schema:
        validate(instance, _resolve(root, schema["$ref"]), root, path)
    if "type" in schema:
        names = schema["type"] if isinstance(schema["type"], list) else [schema["type"]]
        if not any(_type_matches(instance, name) for name in names):
            raise ValidationError(path, f"expected {'|'.join(names)}")
    if "const" in schema:
        expected = schema["const"]
        if instance != expected or type(instance) is not type(expected):
            raise ValidationError(path, f"must equal {expected!r}")
    if "enum" in schema and not any(
        instance == option and type(instance) is type(option) for option in schema["enum"]
    ):
        raise ValidationError(path, "value is not in the allowed set")
    if isinstance(instance, str):
        if "pattern" in schema and not re.search(schema["pattern"], instance):
            raise ValidationError(path, "does not match the required pattern")
        if "minLength" in schema and len(instance) < schema["minLength"]:
            raise ValidationError(path, "is too short")
        if "maxLength" in schema and len(instance) > schema["maxLength"]:
            raise ValidationError(path, "is too long")
    if _is_integer(instance):
        if "minimum" in schema and instance < schema["minimum"]:
            raise ValidationError(path, f"must be at least {schema['minimum']}")
        if "maximum" in schema and instance > schema["maximum"]:
            raise ValidationError(path, f"must be at most {schema['maximum']}")
    if isinstance(instance, list):
        if "minItems" in schema and len(instance) < schema["minItems"]:
            raise ValidationError(path, "has too few items")
        if "maxItems" in schema and len(instance) > schema["maxItems"]:
            raise ValidationError(path, "has too many items")
        if schema.get("uniqueItems"):
            seen = []
            for item in instance:
                if any(item == other and type(item) is type(other) for other in seen):
                    raise ValidationError(path, "items must be unique")
                seen.append(item)
        if "items" in schema:
            for index, item in enumerate(instance):
                validate(item, schema["items"], root, f"{path}[{index}]")
    if isinstance(instance, dict):
        if "minProperties" in schema and len(instance) < schema["minProperties"]:
            raise ValidationError(path, "has too few properties")
        for name in schema.get("required", []):
            if name not in instance:
                raise ValidationError(path, f"missing required property {name!r}")
        properties = schema.get("properties", {})
        for name, child in instance.items():
            if name in properties:
                validate(child, properties[name], root, f"{path}.{name}")
            elif schema.get("additionalProperties", True) is False:
                raise ValidationError(path, f"unknown property {name!r}")
            elif isinstance(schema.get("additionalProperties"), dict):
                validate(child, schema["additionalProperties"], root, f"{path}.{name}")
    if "oneOf" in schema:
        matches = 0
        for option in schema["oneOf"]:
            try:
                validate(instance, option, root, path)
            except ValidationError:
                continue
            matches += 1
        if matches != 1:
            raise ValidationError(path, f"must match exactly one alternative (matched {matches})")
