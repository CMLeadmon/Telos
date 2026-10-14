#!/usr/bin/env python3
"""Mutation tests for the monitoring verifier: configuration (source) and a running
Prometheus (runtime, judged from fixture API responses)."""

import copy
import json
import shutil
import sys
import tempfile
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "lib"))
sys.dont_write_bytecode = True
import monitoring_check as mc  # noqa: E402
import yaml  # noqa: E402

LOCK = json.loads((ROOT / "release" / "images.lock").read_text(encoding="utf-8"))
FILES = ["config/prometheus/prometheus.yml", "config/prometheus/alerts.yml", "documentation/operations/incidents.md",
         "docker-compose.yml", "config/dynamic/routes.yaml"]


class Workspace:
    """A scratch copy of the files the source checks read, plus the Go metrics code."""

    def __init__(self):
        self.dir = Path(tempfile.mkdtemp())
        for relative in FILES:
            target = self.dir / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(ROOT / relative, target)
        (self.dir / "backend").mkdir()
        for path in (ROOT / "backend").glob("*.go"):
            if not path.name.endswith("_test.go"):
                shutil.copy(path, self.dir / "backend" / path.name)

    def edit(self, relative, fn):
        path = self.dir / relative
        path.write_text(fn(path.read_text(encoding="utf-8")), encoding="utf-8")

    def edit_yaml(self, relative, fn):
        path = self.dir / relative
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
        fn(document)
        path.write_text(yaml.safe_dump(document, sort_keys=False), encoding="utf-8")

    def findings(self):
        return mc.check_source(self.dir, LOCK)

    def close(self):
        shutil.rmtree(self.dir, ignore_errors=True)


class SourceChecks(unittest.TestCase):
    def setUp(self):
        self.ws = Workspace()
        self.addCleanup(self.ws.close)

    def expect(self, fragment):
        findings = self.ws.findings()
        self.assertTrue(any(fragment in item for item in findings), f"{fragment!r} not reported in {findings}")

    def first_rule(self, document):
        return document["groups"][0]["rules"][0]

    def test_the_repository_is_clean(self):
        self.assertEqual(self.ws.findings(), [])

    def test_alert_rules(self):
        self.ws.edit("config/prometheus/alerts.yml", lambda t: t.replace("up{job=\"telos-core\"} == 0", "telos_active_sockets < 0", 1))
        self.expect("can never be true")

    def test_severity_for_and_runbook(self):
        self.ws.edit_yaml("config/prometheus/alerts.yml", lambda d: d["groups"][0]["rules"][0]["labels"].__setitem__("severity", "page"))
        self.expect("severity must be warning or critical")

    def test_missing_for_duration(self):
        self.ws.edit_yaml("config/prometheus/alerts.yml", lambda d: d["groups"][0]["rules"][0].pop("for"))
        self.expect("needs a 'for' duration")

    def test_runbook_anchor_without_a_heading(self):
        self.ws.edit_yaml("config/prometheus/alerts.yml", lambda d: d["groups"][0]["rules"][0]["annotations"].__setitem__("runbook_url", "documentation/operations/incidents.md#nowhere"))
        self.expect("has no heading in incidents.md")

    def test_runbook_url_must_be_in_the_repository(self):
        self.ws.edit_yaml("config/prometheus/alerts.yml", lambda d: d["groups"][0]["rules"][0]["annotations"].__setitem__("runbook_url", "https://example.com/wiki"))
        self.expect("runbook_url must be")

    def test_series_the_gateway_does_not_export(self):
        self.ws.edit("config/prometheus/alerts.yml", lambda t: t.replace("telos_outbox_queue_size > 100", "telos_invented_series > 100", 1))
        self.expect("telos_invented_series")

    def test_duplicate_and_missing_required_alerts(self):
        def duplicate(document):
            rules = document["groups"][0]["rules"]
            rules.append(copy.deepcopy(rules[0]))
        self.ws.edit_yaml("config/prometheus/alerts.yml", duplicate)
        self.expect("defined twice")
        shutil.copy(ROOT / "config/prometheus/alerts.yml", self.ws.dir / "config/prometheus/alerts.yml")
        self.ws.edit_yaml("config/prometheus/alerts.yml", lambda d: d["groups"][0]["rules"].pop(0))
        self.expect("must define TelosScrapeDown")

    def test_metrics_network_and_publication(self):
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["networks"]["telos-observability"].pop("internal"))
        self.expect("must be internal")
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["prometheus"].__setitem__("ports", ["9090:9090"]))
        self.expect("publishes a port mentioning 9090")
        self.expect("prometheus must publish no port")
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["telos-core"].__setitem__("ports", ["127.0.0.1:9090:9090"]))
        self.expect("telos-core: publishes a port")

    def test_only_two_services_join_the_metrics_network(self):
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["redis"]["networks"].append("telos-observability"))
        self.expect("only telos-core and prometheus may join")

    def test_prometheus_must_be_locked_and_bounded(self):
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["prometheus"].__setitem__("image", "docker.io/prom/prometheus:latest"))
        self.expect("locked, digest-pinned image")
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["prometheus"].__setitem__("command", ["--config.file=/etc/prometheus/prometheus.yml"]))
        self.expect("retention must be bounded")
        self.ws.edit_yaml("docker-compose.yml", lambda d: d["services"]["prometheus"].pop("healthcheck"))
        self.expect("needs a health check")
        for flag in ("--web.enable-lifecycle", "--web.enable-admin-api"):
            shutil.copy(ROOT / "docker-compose.yml", self.ws.dir / "docker-compose.yml")
            self.ws.edit_yaml("docker-compose.yml", lambda d, f=flag: d["services"]["prometheus"]["command"].append(f))
            self.expect(f"must not enable its {flag.split('enable-')[1]}")

    def test_metrics_are_never_routed_publicly(self):
        for token in ("/metrics", "prometheus", "9090"):
            self.ws.edit("config/dynamic/routes.yaml", lambda t, token=token: t + f"\n# {token}\n")
            self.expect(f"mentions {token!r}")
            shutil.copy(ROOT / "config/dynamic/routes.yaml", self.ws.dir / "config/dynamic/routes.yaml")

    def test_scrape_targets(self):
        self.ws.edit_yaml("config/prometheus/prometheus.yml", lambda d: d["scrape_configs"].append({"job_name": "extra", "static_configs": [{"targets": ["telos-postgres:9187"]}]}))
        self.expect("must scrape exactly")
        shutil.copy(ROOT / "config/prometheus/prometheus.yml", self.ws.dir / "config/prometheus/prometheus.yml")
        self.ws.edit_yaml("config/prometheus/prometheus.yml", lambda d: d["scrape_configs"][0]["static_configs"][0].__setitem__("targets", ["telos-core:8080"]))
        self.expect("telos-core:9090 only")

    def test_label_privacy(self):
        for label in ("username", "path", "channel", "item_id", "client_ip", "token"):
            self.ws.edit("backend/metrics.go", lambda t, label=label: t.replace('[]string{"method", "code_class"}', f'[]string{{"method", "{label}"}}', 1))
            self.expect(f"label {label!r}")
            shutil.copy(ROOT / "backend/metrics.go", self.ws.dir / "backend/metrics.go")


def healthy(now):
    return {
        "targets": {"activeTargets": [{"labels": {"job": "telos-core"}, "scrapeUrl": "http://telos-core:9090/metrics", "health": "up",
                                       "lastError": "", "scrapeInterval": "15s", "lastScrape": time.strftime("%Y-%m-%dT%H:%M:%S.000000000Z", time.gmtime(now - 5))}]},
        "rules": {"groups": [{"name": "telos", "rules": [{"name": "TelosScrapeDown", "health": "ok"}]}]},
        "alerts": {"alerts": [{"state": "pending", "labels": {"alertname": "TelosOutboxBacklog", "severity": "warning"}}]},
        "series": {query: [{"metric": {}, "value": [now, "1"]}] for query in mc.REQUIRED_SERIES},
    }


class RuntimeChecks(unittest.TestCase):
    now = 1_790_000_000.0

    def judge(self, mutate=lambda d: None):
        data = healthy(self.now)
        mutate(data)
        return mc.evaluate_runtime(data["targets"], data["rules"], data["alerts"], data["series"], self.now)

    def expect(self, mutate, fragment):
        findings = self.judge(mutate)
        self.assertTrue(any(fragment in item for item in findings), f"{fragment!r} not reported in {findings}")

    def test_a_healthy_node_passes(self):
        self.assertEqual(self.judge(), [])

    def test_a_down_target_fails(self):
        self.expect(lambda d: d["targets"]["activeTargets"][0].update(health="down", lastError="connection refused"), "is down")

    def test_a_stale_scrape_fails(self):
        self.expect(lambda d: d["targets"]["activeTargets"][0].update(lastScrape=time.strftime("%Y-%m-%dT%H:%M:%S.000000000Z", time.gmtime(self.now - 600))), "has not been scraped")
        self.expect(lambda d: d["targets"]["activeTargets"][0].update(lastScrape=""), "has not been scraped")

    def test_no_target_and_no_rules_fail(self):
        self.expect(lambda d: d["targets"].update(activeTargets=[]), "no active target")
        self.expect(lambda d: d["rules"].update(groups=[]), "no alert rules")

    def test_a_broken_rule_fails(self):
        self.expect(lambda d: d["rules"]["groups"][0]["rules"][0].update(health="err", lastError="bad expression"), "is err")

    def test_a_firing_critical_alert_fails_but_a_warning_does_not(self):
        self.expect(lambda d: d["alerts"]["alerts"].append({"state": "firing", "labels": {"alertname": "TelosDependencyDown", "severity": "critical"}}), "critical alert TelosDependencyDown is firing")
        self.assertEqual(self.judge(lambda d: d["alerts"]["alerts"].append({"state": "firing", "labels": {"alertname": "TelosOutboxBacklog", "severity": "warning"}})), [])
        self.assertEqual(self.judge(lambda d: d["alerts"]["alerts"].append({"state": "pending", "labels": {"alertname": "TelosScrapeDown", "severity": "critical"}})), [])

    def test_every_required_series_must_have_data(self):
        for query in mc.REQUIRED_SERIES:
            self.expect(lambda d, q=query: d["series"].__setitem__(q, []), f"required series {query} has no data")


if __name__ == "__main__":
    unittest.main(verbosity=2)
