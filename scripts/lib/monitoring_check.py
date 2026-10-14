#!/usr/bin/env python3
"""Pure checks for the monitoring configuration and a running Prometheus.

``check_source`` reads the repository: alert rules, scrape configuration, Compose
routing and the metric label vocabulary. ``evaluate_runtime`` judges the JSON
Prometheus returns from its target, rule and alert APIs plus a handful of instant
queries. Neither prints or exits; both return findings, so the tests can feed them
hostile documents and the CLI decides what to do.
"""

import re
from pathlib import Path

import yaml

ALLOWED_SEVERITIES = {"warning", "critical"}
ALLOWED_LABELS = {"method", "code_class", "service", "provider", "outcome", "operation", "result"}
FORBIDDEN_LABEL_WORDS = ("user", "username", "channel", "item", "query", "host", "token", "path", "ip", "address", "email")
PROMETHEUS_PORT = "9090"
SCRAPE_JOB = "telos-core"
REQUIRED_SERIES = (
    'up{job="telos-core"}',
    "telos_dependency_up",
    "telos_http_requests_total",
    "telos_active_sockets",
    "telos_outbox_queue_size",
    "telos_backup_status_valid",
)
SCRAPE_STALE_FACTOR = 3  # a target not scraped for three intervals is stale


def slugify(heading):
    """GitHub's heading anchor: lowercase, drop punctuation, spaces to hyphens."""
    text = re.sub(r"[^\w\- ]", "", heading.strip().lower())
    return re.sub(r" ", "-", text)


def declared_metric_names(backend_dir):
    names = set()
    for path in Path(backend_dir).glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        names.update(re.findall(r'Name:\s*"(telos_[a-z0-9_]+)"', text))
        names.update(re.findall(r'(?:Desc\(|const\s+\w+\s*=\s*)"(telos_[a-z0-9_]+)"', text))
        names.update(re.findall(r'^\s*\w+\s*=\s*"(telos_[a-z0-9_]+)"', text, re.M))
        names.update(re.findall(r'NewDesc\(\s*"(telos_[a-z0-9_]+)"', text))
    return names


def declared_label_sets(backend_dir):
    """Every label-name list handed to a *Vec constructor in the metrics code."""
    found = []
    for path in Path(backend_dir).glob("metrics*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        for match in re.finditer(r"New(?:Counter|Gauge|Histogram|Summary)Vec\((.*?)\)\s*\n", text, re.S):
            pass
        for match in re.finditer(r"\}\s*,\s*\[\]string\{([^}]*)\}\s*\)", text):
            found.append((path.name, re.findall(r'"([^"]+)"', match.group(1))))
    return found


def check_alerts(alerts_path, incidents_path, known_series):
    findings = []
    document = yaml.safe_load(Path(alerts_path).read_text(encoding="utf-8")) or {}
    headings = {slugify(m.group(1)) for m in re.finditer(r"^#{1,6}\s+(.+)$", Path(incidents_path).read_text(encoding="utf-8"), re.M)}
    seen = set()
    rules = [rule for group in document.get("groups", []) for rule in group.get("rules", [])]
    if not rules:
        findings.append("alerts.yml defines no rules")
    for rule in rules:
        name = rule.get("alert")
        if not name:
            findings.append("a rule has no alert name")
            continue
        if name in seen:
            findings.append(f"{name}: defined twice")
        seen.add(name)
        expr = str(rule.get("expr", ""))
        severity = (rule.get("labels") or {}).get("severity")
        if severity not in ALLOWED_SEVERITIES:
            findings.append(f"{name}: severity must be warning or critical, not {severity!r}")
        if not rule.get("for"):
            findings.append(f"{name}: needs a 'for' duration so one bad scrape cannot page")
        url = (rule.get("annotations") or {}).get("runbook_url", "")
        match = re.fullmatch(r"documentation/operations/incidents\.md#([a-z0-9-]+)", url)
        if not match:
            findings.append(f"{name}: runbook_url must be documentation/operations/incidents.md#<anchor>")
        elif match.group(1) not in headings:
            findings.append(f"{name}: runbook anchor #{match.group(1)} has no heading in incidents.md")
        if re.search(r"(?<![=!<>])\s<\s*0(?![0-9.])", expr):
            findings.append(f"{name}: compares a non-negative series with < 0, which can never be true")
        for series in re.findall(r"\b(telos_[a-z0-9_]+)\b", expr):
            base = re.sub(r"_(bucket|sum|count|total)$", "", series) if series not in known_series else series
            if series not in known_series and base not in known_series and f"{base}_total" not in known_series:
                findings.append(f"{name}: references {series}, which the gateway does not export")
        if "up{" in expr and f'job="{SCRAPE_JOB}"' not in expr and "up == 0" not in expr:
            findings.append(f"{name}: an 'up' alert must name the {SCRAPE_JOB} job")
    if "TelosScrapeDown" not in seen or "TelosDependencyDown" not in seen:
        findings.append("alerts.yml must define TelosScrapeDown and TelosDependencyDown")
    return findings


def check_compose_routing(compose_path, routes_path, lock):
    findings = []
    compose = yaml.safe_load(Path(compose_path).read_text(encoding="utf-8")) or {}
    services = compose.get("services", {})
    networks = compose.get("networks", {})
    observability = networks.get("telos-observability")
    if not observability:
        findings.append("the telos-observability network is not defined")
    elif observability.get("internal") is not True:
        findings.append("telos-observability must be internal (no route out, no published ports)")
    members = sorted(
        name for name, service in services.items()
        if "telos-observability" in (service.get("networks") if isinstance(service.get("networks"), list) else (service.get("networks") or {}).keys())
    )
    if members != ["prometheus", "telos-core"]:
        findings.append(f"only telos-core and prometheus may join telos-observability, found {members}")
    for name, service in services.items():
        for port in service.get("ports") or []:
            text = str(port)
            if PROMETHEUS_PORT in text.replace('"', ""):
                findings.append(f"{name}: publishes a port mentioning {PROMETHEUS_PORT} ({text}); metrics are never published")
    prometheus = services.get("prometheus")
    if not prometheus:
        findings.append("the prometheus service is missing")
    else:
        if prometheus.get("ports"):
            findings.append("prometheus must publish no port")
        entry = lock.get("images", {}).get("prometheus")
        expected = f"{entry['source']}@{entry['manifestDigest']}" if entry else None
        if expected is None or prometheus.get("image") != expected:
            findings.append("prometheus must use the locked, digest-pinned image")
        command = " ".join(str(item) for item in prometheus.get("command", []))
        if not re.search(r"--storage\.tsdb\.retention\.time=\d+d", command) or "--storage.tsdb.retention.size=" not in command:
            findings.append("prometheus retention must be bounded by time and size")
        if "--web.enable-lifecycle=false" not in command and "--web.enable-lifecycle" in command:
            findings.append("prometheus must not enable its lifecycle API")
        if not prometheus.get("healthcheck"):
            findings.append("prometheus needs a health check")
    core = services.get("telos-core", {})
    environment = " ".join(str(item) for item in core.get("environment", []))
    if "METRICS_ADDR=" not in environment:
        findings.append("telos-core must set METRICS_ADDR")
    routes = Path(routes_path).read_text(encoding="utf-8")
    for token in ("9090", "prometheus", "/metrics"):
        if token in routes.lower():
            findings.append(f"config/dynamic/routes.yaml mentions {token!r}; metrics must never be routed publicly")
    return findings


def check_privacy(backend_dir):
    findings = []
    for filename, labels in declared_label_sets(backend_dir):
        for label in labels:
            if label not in ALLOWED_LABELS:
                findings.append(f"{filename}: label {label!r} is outside the allowed vocabulary {sorted(ALLOWED_LABELS)}")
            for word in FORBIDDEN_LABEL_WORDS:
                if re.search(rf"(^|_){word}($|_)", label):
                    findings.append(f"{filename}: label {label!r} names {word}")
    return findings


def check_source(root, lock):
    root = Path(root)
    known = declared_metric_names(root / "backend")
    findings = []
    findings += check_alerts(root / "config/prometheus/alerts.yml", root / "documentation/operations/incidents.md", known)
    findings += check_compose_routing(root / "docker-compose.yml", root / "config/dynamic/routes.yaml", lock)
    findings += check_privacy(root / "backend")
    scrape = yaml.safe_load((root / "config/prometheus/prometheus.yml").read_text(encoding="utf-8")) or {}
    jobs = {job.get("job_name"): job for job in scrape.get("scrape_configs", [])}
    if set(jobs) != {SCRAPE_JOB}:
        findings.append(f"prometheus.yml must scrape exactly the {SCRAPE_JOB} job, found {sorted(jobs)}")
    else:
        targets = [t for config in jobs[SCRAPE_JOB].get("static_configs", []) for t in config.get("targets", [])]
        if targets != ["telos-core:9090"]:
            findings.append(f"the {SCRAPE_JOB} job must scrape telos-core:9090 only, found {targets}")
    for series in REQUIRED_SERIES:
        base = re.match(r"[a-z_0-9]+", series).group(0)
        if base.startswith("telos_") and base not in known:
            findings.append(f"required series {base} is not exported by the gateway")
    return findings


def evaluate_runtime(targets, rules, alerts, series, now):
    """Judge Prometheus's own view. All inputs are the parsed API ``data`` objects.

    ``series`` maps each required query to the list of instant-vector results.
    """
    findings = []
    active = [t for t in (targets or {}).get("activeTargets", []) if t.get("labels", {}).get("job") == SCRAPE_JOB]
    if not active:
        findings.append(f"no active target for job {SCRAPE_JOB}")
    for target in active:
        if target.get("health") != "up":
            findings.append(f"target {target.get('scrapeUrl', '?')} is {target.get('health')}: {str(target.get('lastError'))[:120]}")
        interval = _seconds(target.get("scrapeInterval"), 15)
        scraped = _parse_time(target.get("lastScrape"))
        if scraped is None or now - scraped > SCRAPE_STALE_FACTOR * interval + 5:
            findings.append(f"target {target.get('scrapeUrl', '?')} has not been scraped for more than {SCRAPE_STALE_FACTOR} intervals")
    groups = (rules or {}).get("groups", [])
    if not groups:
        findings.append("Prometheus has loaded no alert rules")
    for group in groups:
        for rule in group.get("rules", []):
            if rule.get("health") not in (None, "ok"):
                findings.append(f"rule {rule.get('name')} is {rule.get('health')}: {str(rule.get('lastError'))[:120]}")
    for alert in (alerts or {}).get("alerts", []):
        if alert.get("state") == "firing" and (alert.get("labels") or {}).get("severity") == "critical":
            findings.append(f"critical alert {alert.get('labels', {}).get('alertname')} is firing")
    for query in REQUIRED_SERIES:
        if not series.get(query):
            findings.append(f"required series {query} has no data")
    return findings


def _seconds(text, default):
    match = re.fullmatch(r"(\d+)s", str(text or ""))
    return int(match.group(1)) if match else default


def _parse_time(text):
    import datetime
    if not text:
        return None
    try:
        cleaned = re.sub(r"(\.\d{6})\d+", r"\1", str(text)).replace("Z", "+00:00")
        return datetime.datetime.fromisoformat(cleaned).timestamp()
    except ValueError:
        return None
