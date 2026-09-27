#!/usr/bin/env python3
"""Generate promtool fixtures for the KME storage-class latency alerts.

Scenarios, all in namespace "ns":

- px-pct: 3 of 4 VMIs have a write tail above 100ms, reads are fast.
  Percentage branch fires (75% and at least 3). Read does not fire.
- px-few: 2 of 4 VMIs are slow. Above 10% but below the minimum of 3.
- px-wide: 11 of 111 VMIs are slow (under 10%). Count branch fires.
- px-quiet: 3 VMIs are slow but have fewer than 100 operations in 5m.
- px-flush: 3 VMIs have a flush tail above 1s.
- px-flush-mild: 3 VMIs have a flush tail above 100ms and below 1s.
- px-block: 3 of 4 VMIs are slow at the block layer, plus one slow pod
  that is not a running VMI.
- px-nfs: 3 of 4 VMIs have an NFS tail above 1s.
- px-nfs-mild: 3 VMIs have an NFS tail above 100ms and below 1s.
- px-guest: 3 of 4 VMIs have guest average latency above 100ms.
"""

import pathlib

import yaml

ROOT = pathlib.Path(__file__).resolve().parents[2]
RULE_SOURCE = ROOT / "assets/active/kubevirt-metrics-exporter/prometheusrule.yaml"
RULES_OUT = pathlib.Path(__file__).resolve().parent / "kme-prometheus-rules.yml"
TEST_OUT = pathlib.Path(__file__).resolve().parent / "kme_storage_latency_alert_tests.yml"

STEPS = 60
NAMESPACE = "ns"


def counter(step):
    return "0+%dx%d" % (step, STEPS)


def gauge(value):
    return "%s+0x%d" % (value, STEPS)


def main():
    document = yaml.safe_load(RULE_SOURCE.read_text())
    groups = [
        group
        for group in document["spec"]["groups"]
        if group["name"].startswith("kubevirt-storage-io")
    ]
    alert_names = {
        rule["alert"]
        for group in groups
        for rule in group["rules"]
        if "alert" in rule
    }
    removed = {
        "VMIStorageWriteLatencyHigh",
        "VMIStorageReadLatencyHigh",
        "VMIStorageFlushLatencyHigh",
        "VMIGuestStorageLatencyHigh",
        "PVCBlockLatencyHigh",
        "PVCNFSLatencyHigh",
    }
    missing = removed & alert_names
    if missing:
        raise SystemExit("per-VMI latency alerts still present: %s" % sorted(missing))
    required = {
        "StorageClassVMILatencyHigh",
        "StorageClassVMIFlushLatencyHigh",
        "StorageClassBlockLatencyHigh",
        "StorageClassNFSLatencyHigh",
        "StorageClassGuestLatencyHigh",
        "NodeVMStorageLatencyWidespread",
        "VMIDiskSaturated",
    }
    absent = required - alert_names
    if absent:
        raise SystemExit("expected alerts missing: %s" % sorted(absent))

    RULES_OUT.write_text(yaml.safe_dump({"groups": groups}, sort_keys=False))

    series = {}

    def add(name, values):
        existing = series.get(name)
        if existing is not None and existing != values:
            raise SystemExit("duplicate series %s" % name)
        series[name] = values

    def pvc(storage_class, claim):
        add(
            'kube_persistentvolumeclaim_info{namespace="%s",persistentvolumeclaim="%s",storageclass="%s"}'
            % (NAMESPACE, claim, storage_class),
            gauge(1),
        )

    def qmp(name, claim, operation, count_step, bucket_step, le):
        labels = (
            'namespace="%s",name="%s",disk="root",persistentvolumeclaim="%s",operation="%s"'
            % (NAMESPACE, name, claim, operation)
        )
        add(
            "kubevirt_vmi_storage_io_latency_seconds_count{%s}" % labels,
            counter(count_step),
        )
        add(
            'kubevirt_vmi_storage_io_latency_seconds_bucket{%s,le="%s"}' % (labels, le),
            counter(bucket_step),
        )

    def pod_hist(metric, pod, claim, operation, count_step, bucket_step, le):
        labels = (
            'namespace="%s",pod="%s",persistentvolumeclaim="%s",operation="%s"'
            % (NAMESPACE, pod, claim, operation)
        )
        add("%s_count{%s}" % (metric, labels), counter(count_step))
        add('%s_bucket{%s,le="%s"}' % (metric, labels, le), counter(bucket_step))

    def vmi(name, pod):
        add(
            'kubevirt_vmi_info{namespace="%s",name="%s",vmi_pod="%s",phase="running"}'
            % (NAMESPACE, name, pod),
            gauge(1),
        )

    def guest(name, claim, operation, value):
        add(
            'kubevirt_vmi_storage_guest_latency_avg_seconds{namespace="%s",name="%s",disk="root",persistentvolumeclaim="%s",operation="%s"}'
            % (NAMESPACE, name, claim, operation),
            gauge(value),
        )

    def vms(storage_class, prefix, slow, fast, operation="write", le="0.1", kind="qmp"):
        for index in range(slow + fast):
            name = "%s-%d" % (prefix, index)
            claim = "pvc-%s" % name
            pod = "virt-launcher-%s" % name
            is_slow = index < slow
            count_step = 100
            bucket_step = 90 if is_slow else 100
            pvc(storage_class, claim)
            if kind == "qmp":
                qmp(name, claim, operation, count_step, bucket_step, le)
            elif kind == "block":
                pod_hist(
                    "kme_block_io_latency_seconds",
                    pod,
                    claim,
                    operation,
                    count_step,
                    bucket_step,
                    le,
                )
                vmi(name, pod)
            elif kind == "nfs":
                pod_hist(
                    "kme_nfs_io_latency_seconds",
                    pod,
                    claim,
                    operation,
                    count_step,
                    bucket_step,
                    le,
                )
                vmi(name, pod)
            else:
                raise SystemExit("unknown kind %s" % kind)

    vms("px-pct", "pct", slow=3, fast=1, operation="write", le="0.1")
    vms("px-pct", "pct", slow=0, fast=4, operation="read", le="0.1")
    vms("px-few", "few", slow=2, fast=2, operation="write", le="0.1")
    vms("px-wide", "wide", slow=11, fast=100, operation="write", le="0.1")
    for index in range(3):
        name = "quiet-%d" % index
        claim = "pvc-%s" % name
        pvc("px-quiet", claim)
        qmp(name, claim, "write", count_step=10, bucket_step=0, le="0.1")

    vms("px-flush", "flush", slow=3, fast=0, operation="flush", le="1")
    vms("px-flush-mild", "flush-mild", slow=0, fast=3, operation="flush", le="1")
    for index in range(3):
        # Tail sits between 100ms and 1s. The 1s bucket still matches the count.
        name = "flush-mild-%d" % index
        qmp(name, "pvc-%s" % name, "flush", count_step=100, bucket_step=90, le="0.1")

    vms("px-block", "block", slow=3, fast=1, operation="write", le="0.1", kind="block")
    pvc("px-block", "pvc-block-orphan")
    pod_hist(
        "kme_block_io_latency_seconds",
        "orphan-pod",
        "pvc-block-orphan",
        "write",
        100,
        90,
        "0.1",
    )

    vms("px-nfs", "nfs", slow=3, fast=1, operation="write", le="1", kind="nfs")
    vms("px-nfs-mild", "nfs-mild", slow=0, fast=3, operation="write", le="1", kind="nfs")
    for index in range(3):
        name = "nfs-mild-%d" % index
        pod_hist(
            "kme_nfs_io_latency_seconds",
            "virt-launcher-%s" % name,
            "pvc-%s" % name,
            "write",
            100,
            90,
            "0.1",
        )

    for index, value in enumerate((0.2, 0.2, 0.2, 0.01)):
        name = "guest-%d" % index
        claim = "pvc-%s" % name
        pvc("px-guest", claim)
        guest(name, claim, "write", value)

    lines = [
        "# Generated by test/promtool/generate_kme_latency_tests.py.",
        "# Do not edit. Regenerate with hack/test-alert-rules.sh.",
        "rule_files:",
        "  - kme-prometheus-rules.yml",
        "",
        "evaluation_interval: 1m",
        "",
        "tests:",
        "  - interval: 1m",
        "    input_series:",
    ]
    for name, values in series.items():
        lines.append("      - series: '%s'" % name)
        lines.append("        values: '%s'" % values)

    lines.extend(
        [
            "",
            "    promql_expr_test:",
            "      - expr: storageclass:kme_vmi_io_latency_affected:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_vmi_io_latency_affected:count",
                [
                    ("px-pct", "write", 3),
                    ("px-few", "write", 2),
                    ("px-wide", "write", 11),
                ],
            ),
            "      - expr: storageclass:kme_vmi_io_latency_active:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_vmi_io_latency_active:count",
                [
                    ("px-pct", "read", 4),
                    ("px-pct", "write", 4),
                    ("px-few", "write", 4),
                    ("px-wide", "write", 111),
                ],
            ),
            "      - expr: storageclass:kme_vmi_flush_latency_affected:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_vmi_flush_latency_affected:count",
                [("px-flush", "flush", 3)],
            ),
            "      - expr: storageclass:kme_block_io_latency_affected:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_block_io_latency_affected:count",
                [("px-block", "write", 3)],
            ),
            "      - expr: storageclass:kme_block_io_latency_active:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_block_io_latency_active:count",
                [("px-block", "write", 4)],
            ),
            "      - expr: storageclass:kme_nfs_io_latency_affected:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_nfs_io_latency_affected:count",
                [("px-nfs", "write", 3)],
            ),
            "      - expr: storageclass:kme_guest_latency_affected:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_guest_latency_affected:count",
                [("px-guest", "write", 3)],
            ),
            "      - expr: storageclass:kme_guest_latency_active:count",
            "        eval_time: 20m",
            "        exp_samples:",
            *samples(
                "storageclass:kme_guest_latency_active:count",
                [("px-guest", "write", 4)],
            ),
            "",
            "    alert_rule_test:",
            "      - eval_time: 8m",
            "        alertname: StorageClassVMILatencyHigh",
            "        exp_alerts: []",
            "      - eval_time: 40m",
            "        alertname: StorageClassVMILatencyHigh",
            "        exp_alerts:",
            *alert(
                "High write latency on storage class px-pct",
                qmp_description(3, "px-pct", "write", "100ms"),
                "px-pct",
                "write",
            ),
            *alert(
                "High write latency on storage class px-wide",
                qmp_description(11, "px-wide", "write", "100ms"),
                "px-wide",
                "write",
            ),
            "      - eval_time: 40m",
            "        alertname: StorageClassVMIFlushLatencyHigh",
            "        exp_alerts:",
            *alert(
                "High flush latency on storage class px-flush",
                flush_description(3, "px-flush"),
                "px-flush",
                "flush",
            ),
            "      - eval_time: 40m",
            "        alertname: StorageClassBlockLatencyHigh",
            "        exp_alerts:",
            *alert(
                "High block write latency on storage class px-block",
                block_description(3, "px-block", "write"),
                "px-block",
                "write",
            ),
            "      - eval_time: 40m",
            "        alertname: StorageClassNFSLatencyHigh",
            "        exp_alerts:",
            *alert(
                "High NFS write latency on storage class px-nfs",
                nfs_description(3, "px-nfs", "write"),
                "px-nfs",
                "write",
            ),
            "      - eval_time: 40m",
            "        alertname: StorageClassGuestLatencyHigh",
            "        exp_alerts:",
            *alert(
                "High guest write latency on storage class px-guest",
                guest_description(3, "px-guest", "write"),
                "px-guest",
                "write",
            ),
            "",
        ]
    )
    TEST_OUT.write_text("\n".join(lines))
    print("wrote %s" % RULES_OUT)
    print("wrote %s (%d series)" % (TEST_OUT, len(series)))


def samples(metric, rows):
    rendered = []
    for storage_class, operation, value in rows:
        rendered.append(
            "          - labels: '%s{operation=\"%s\",storageclass=\"%s\"}'"
            % (metric, operation, storage_class)
        )
        rendered.append("            value: %d" % value)
    return rendered


def alert(summary, description, storage_class, operation):
    return [
        "          - exp_labels:",
        "              severity: warning",
        "              namespace: kubevirt-metrics-exporter",
        "              operation: %s" % operation,
        "              storageclass: %s" % storage_class,
        "            exp_annotations:",
        "              summary: %s" % yaml_quote(summary),
        "              description: %s" % yaml_quote(description),
    ]


def yaml_quote(text):
    return yaml.safe_dump(text, default_style='"').strip()


def qmp_description(count, storage_class, operation, threshold):
    return (
        "%d VMIs on storage class %s have more than 1%% of %s operations slower than %s. "
        "Fires when more than 10 VMIs are affected, or more than 10%% of active VMIs are affected and at least 3 are affected."
        % (count, storage_class, operation, threshold)
    )


def flush_description(count, storage_class):
    return (
        "%d VMIs on storage class %s have more than 1%% of flush operations slower than 1s. "
        "Durability-sensitive workloads on this storage will see slow commits. "
        "Fires when more than 10 VMIs are affected, or more than 10%% of active VMIs are affected and at least 3 are affected."
        % (count, storage_class)
    )


def block_description(count, storage_class, operation):
    return (
        "%d VMIs on storage class %s have more than 1%% of block %s operations slower than 100ms. "
        "Fires when more than 10 VMIs are affected, or more than 10%% of active VMIs are affected and at least 3 are affected."
        % (count, storage_class, operation)
    )


def nfs_description(count, storage_class, operation):
    return (
        "%d VMIs on storage class %s have more than 1%% of NFS %s operations slower than 1s. "
        "Fires when more than 10 VMIs are affected, or more than 10%% of active VMIs are affected and at least 3 are affected."
        % (count, storage_class, operation)
    )


def guest_description(count, storage_class, operation):
    return (
        "%d VMIs on storage class %s have average guest %s latency above 100ms. "
        "Fires when more than 10 VMIs are affected, or more than 10%% of VMIs reporting guest latency on that storage class are affected and at least 3 are affected."
        % (count, storage_class, operation)
    )


if __name__ == "__main__":
    main()
