#!/usr/bin/env python3
"""
Shutdown inhibitor daemon.

Takes a delay inhibitor lock via systemd-inhibit, then watches for the
PrepareForShutdown(true) D-Bus signal from logind. When the PrepareForShutdown
signal is observed, it finds all pods that appear to contain running kubevirt VMs
and uses crictl exec to issue virsh shutdowns.

"""

import json
import logging
import signal
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

log = logging.getLogger("shutdown-vms")

TIMEOUT_SEC = 120
POLL_SEC = 2
PARALLEL = 32

class VirtLauncherPod:
    def __init__(self, pod_id, pod_name, namespace, container_id=""):
        self.pod_id = pod_id
        self.pod_name = pod_name
        self.namespace = namespace
        self.container_id = container_id


class VMInfo:
    def __init__(self, domain_name, state, pod):
        self.domain_name = domain_name
        self.state = state
        self.pod = pod


def run_cmd(args, *, timeout=30, check=True):
    log.debug("Running: %s", " ".join(args))
    try:
        proc = subprocess.run(
            args,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as exc:
        log.error("Command timed out after %ds: %s", timeout, " ".join(args))
        raise SystemExit(1) from exc

    if proc.stdout:
        log.debug("stdout: %s", proc.stdout.rstrip())
    if proc.stderr:
        log.debug("stderr: %s", proc.stderr.rstrip())

    if check and proc.returncode != 0:
        log.error(
            "Command failed (rc=%d): %s\n%s",
            proc.returncode,
            " ".join(args),
            proc.stderr.rstrip(),
        )
        raise subprocess.CalledProcessError(
            proc.returncode, args, proc.stdout, proc.stderr
        )
    return proc


def discover_pods():
    proc = run_cmd(
        ["crictl", "pods", "--label", "kubevirt.io=virt-launcher", "-o", "json"],
    )
    data = json.loads(proc.stdout)
    items = data.get("items") or []

    pods = []
    for item in items:
        state = item.get("state", "")
        if state != "SANDBOX_READY":
            pod_name = item.get("metadata", {}).get("name", item.get("id", "?"))
            log.warning("Skipping pod %s in state %s", pod_name, state)
            continue

        metadata = item.get("metadata", {})
        pod = VirtLauncherPod(
            pod_id=item["id"],
            pod_name=metadata.get("name", ""),
            namespace=metadata.get("namespace", ""),
        )
        pods.append(pod)

    return pods


def resolve_container_id(pod):
    proc = run_cmd(
        ["crictl", "ps", "--pod", pod.pod_id, "-o", "json"],
        check=False,
    )
    if proc.returncode != 0:
        log.warning("Failed to list containers for pod %s", pod.pod_name)
        return False

    data = json.loads(proc.stdout)
    containers = data.get("containers") or []

    for container in containers:
        metadata = container.get("metadata", {})
        name = metadata.get("name", "")
        state = container.get("state", "")
        if name == "compute" and state == "CONTAINER_RUNNING":
            pod.container_id = container["id"]
            return True

    log.warning("No running compute container in pod %s", pod.pod_name)
    return False


def get_vm_domain(pod):
    try:
        proc = run_cmd(
            ["crictl", "exec", pod.container_id, "virsh", "list", "--name"],
        )
    except subprocess.CalledProcessError:
        log.warning("virsh list failed in pod %s", pod.pod_name)
        return None

    names = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not names:
        log.warning("No VM domains found in pod %s", pod.pod_name)
        return None

    return names[0]


def get_domain_state(pod, domain):
    proc = run_cmd(
        ["crictl", "exec", pod.container_id, "virsh", "domstate", domain],
        check=False,
    )
    if proc.returncode != 0:
        return "unknown"
    return proc.stdout.strip()


def shutdown_vm(vm, *, timeout, poll_interval):
    """Use crictl exec to issue a virsh shutdown to the VM."""
    pod = vm.pod
    label = f"{pod.namespace}/{pod.pod_name} (domain: {vm.domain_name})"
    log.info("Shutting down %s", label)
    try:
        run_cmd(
            ["crictl", "exec", pod.container_id, "virsh", "shutdown", vm.domain_name],
        )
    except subprocess.CalledProcessError:
        log.error("virsh shutdown failed for %s", label)
        return

    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        state = get_domain_state(pod, vm.domain_name)
        if state == "shut off":
            log.info("VM %s is shut off", label)
            return
        log.debug("VM %s state: %s — waiting...", label, state)
        time.sleep(poll_interval)

    state = get_domain_state(pod, vm.domain_name)
    if state == "shut off":
        log.info("VM %s is shut off", label)
    else:
        log.warning("VM %s timed out (state: %s)", label, state)


def shutdown_vms():
    """Find and shutdown running VMs."""
    log.info("Discovering virt-launcher pods...")
    pods = discover_pods()
    if not pods:
        log.info("No virt-launcher pods found on this node.")
        return

    log.info("Found %d virt-launcher pod(s). Resolving containers and VM domains...", len(pods))

    vms = []
    for pod in pods:
        if not resolve_container_id(pod):
            continue
        domain = get_vm_domain(pod)
        if domain is None:
            continue
        state = get_domain_state(pod, domain)
        vms.append(VMInfo(domain_name=domain, state=state, pod=pod))

    if not vms:
        log.info("No running VM domains found.")
        return

    with ThreadPoolExecutor(max_workers=PARALLEL) as pool:
        futures = {
            pool.submit(shutdown_vm, vm, timeout=TIMEOUT_SEC, poll_interval=POLL_SEC): vm for vm in vms
        }
        for future in as_completed(futures):
            future.result()

###################
# Inhibitor Setup #
###################

def take_lock():
    """Acquire a shutdown delay inhibitor.

    systemd-inhibit takes the lock and holds it for the lifetime of the
    wrapped process. We wrap 'sleep infinity' so the lock stays held
    until we explicitly terminate it.
    """
    proc = subprocess.Popen(
        [
            "systemd-inhibit",
            "--what=shutdown",
            "--mode=delay",
            "--who=vm-shutdown-hook",
            "--why=vm-drain",
            "sleep", "infinity",
        ],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
    )
    time.sleep(0.5)
    rc = proc.poll()
    if rc is not None:
        stderr = proc.stderr.read().decode(errors="replace").strip()
        if stderr:
            log.error("systemd-inhibit stderr: %s", stderr)
        raise RuntimeError(
            f"systemd-inhibit exited immediately (rc={rc})")
    log.info("Inhibitor lock acquired (wrapper pid %d)", proc.pid)
    return proc


def release_lock(proc):
    """Release the lock by killing the systemd-inhibit wrapper."""
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
    log.info("Inhibitor lock released")


def start_monitor():
    """Start busctl monitoring for the PrepareForShutdown signal.

    Uses stdbuf to force line-buffered output — without it, libc
    block-buffers pipe output and the signal may never reach us.
    """
    return subprocess.Popen(
        [
            "stdbuf", "-oL",
            "busctl", "monitor", "--system",
            "--match",
            "type='signal',"
            "interface='org.freedesktop.login1.Manager',"
            "member='PrepareForShutdown'",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(name)s [%(levelname)s] %(message)s",
    )

    inhibitor = take_lock()
    monitor = start_monitor()
    monitor_started_at = time.monotonic()

    # sys.exit raises SystemExit, which propagates out of the blocking
    # readline() and triggers the finally block for cleanup.
    def handle_exit(signum, _frame):
        sys.exit(0)

    signal.signal(signal.SIGTERM, handle_exit)
    signal.signal(signal.SIGINT, handle_exit)

    log.info("Watching for PrepareForShutdown signal...")

    # busctl monitor output for a signal looks like:
    #
    #   ‣ Type=signal  Endian=l  ...
    #     Sender=:1.2  Path=...  Member=PrepareForShutdown
    #     MESSAGE "b" {
    #             BOOLEAN true;
    #     };
    #
    # Since our --match filter only passes PrepareForShutdown, any
    # BOOLEAN line we see belongs to that signal.
    retries = 0
    saw_signal_header = False
    try:
        while True:
            line = monitor.stdout.readline()
            if not line:
                stderr_out = monitor.stderr.read()
                monitor.wait()
                if stderr_out:
                    log.warning("busctl stderr: %s",
                                stderr_out.strip())

                retries += 1
                delay = min(retries * 2, 30)
                log.warning("busctl monitor exited — "
                            "retry %d in %ds",
                            retries, delay)
                time.sleep(delay)
                monitor = start_monitor()
                monitor_started_at = time.monotonic()
                saw_signal_header = False
                continue

            retries = 0

            if "PrepareForShutdown" in line:
                saw_signal_header = True
            elif saw_signal_header and "BOOLEAN" in line:
                saw_signal_header = False
                if "true" in line.lower():
                    log.info("Observed shutdown request.")
                    try:
                        shutdown_vms()
                    except Exception:
                        log.exception("Shutting down VMs failed.")
                    release_lock(inhibitor)
                    inhibitor = None
                    break
    finally:
        release_lock(inhibitor)
        if monitor.poll() is None:
            monitor.terminate()
            monitor.wait()


if __name__ == "__main__":
    main()