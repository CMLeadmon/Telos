#!/usr/bin/env python3
"""Test double for `podman compose config`: interpolate a Compose file and print YAML.

This is NOT a Compose implementation and proves nothing about Podman. It exists so
the structural checks in scripts/validate-compose.sh can be mutation-tested on hosts
that have no container runtime. It implements the Compose variable-substitution
forms the repository uses ($$, $VAR, ${VAR}, ${VAR:-d}, ${VAR-d}, ${VAR:?e},
${VAR?e}, nested defaults) and dumps the result. A real provider must still render
the same file for the checks to be meaningful on a node (CI and the Ubuntu host).
"""
import os
import re
import sys

import yaml


class InterpolationError(Exception):
    pass


def _single_quoted(body):
    # python-dotenv: only \\ and \' are escapes inside single quotes.
    return re.sub(r"\\([\\'])", r"\1", body)


def _double_quoted(body):
    escapes = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\", "'": "'", "$": "$"}
    return re.sub(r"\\(.)", lambda m: escapes.get(m.group(1), "\\" + m.group(1)), body)


def load_env(path):
    """Parse a Compose env file with python-dotenv (podman-compose) quoting rules."""
    values = {}
    for raw in open(path, encoding="utf-8").read().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        value = value.strip()
        if value.startswith("'") and value.endswith("'") and len(value) >= 2:
            value = _single_quoted(value[1:-1])
        elif value.startswith('"') and value.endswith('"') and len(value) >= 2:
            value = _double_quoted(value[1:-1])
        values[key.strip()] = value
    return values


def substitute(text, env):
    out, i = [], 0
    while i < len(text):
        char = text[i]
        if char != "$":
            out.append(char)
            i += 1
        elif text[i:i + 2] == "$$":
            out.append("$")
            i += 2
        elif text[i:i + 2] == "${":
            depth, j = 1, i + 2
            while j < len(text) and depth:
                depth += {"{": 1, "}": -1}.get(text[j], 0)
                j += 1
            if depth:
                raise InterpolationError("unterminated ${")
            out.append(expand(text[i + 2:j - 1], env))
            i = j
        else:
            match = re.match(r"[A-Za-z_][A-Za-z0-9_]*", text[i + 1:])
            if not match:
                out.append("$")
                i += 1
            else:
                out.append(env.get(match.group(0), ""))
                i += 1 + len(match.group(0))
    return "".join(out)


def expand(body, env):
    match = re.match(r"([A-Za-z_][A-Za-z0-9_]*)(:?[-?])?(.*)\Z", body, re.S)
    if not match:
        raise InterpolationError(f"invalid substitution ${{{body}}}")
    name, operator, argument = match.groups()
    value = env.get(name)
    if operator is None:
        return value or ""
    colon = operator.startswith(":")
    missing = value is None or (colon and value == "")
    if operator.endswith("-"):
        return substitute(argument, env) if missing else value
    if missing:
        raise InterpolationError(f"{name}: {substitute(argument, env) or 'required variable is missing'}")
    return value


def walk(node, env):
    if isinstance(node, str):
        return substitute(node, env)
    if isinstance(node, list):
        return [walk(item, env) for item in node]
    if isinstance(node, dict):
        return {walk(key, env): walk(value, env) for key, value in node.items()}
    return node


def main(argv):
    env_file, compose_file = None, None
    index = 0
    while index < len(argv):
        if argv[index] == "--env-file":
            env_file = argv[index + 1]
            index += 2
        elif argv[index] == "-f":
            compose_file = argv[index + 1]
            index += 2
        else:
            index += 1
    environment = dict(os.environ)
    environment.update(load_env(env_file) if env_file else {})
    try:
        rendered = walk(yaml.safe_load(open(compose_file or "docker-compose.yml", encoding="utf-8")), environment)
    except InterpolationError as error:
        print(f"compose: {error}", file=sys.stderr)
        return 1
    yaml.safe_dump(rendered, sys.stdout, sort_keys=False)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
