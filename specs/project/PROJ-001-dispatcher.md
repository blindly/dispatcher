---
created: 2026-11-23
id: PROJ-001
status: implemented
title: dispatcher
type: project
---

# dispatcher

## Overview

Dispatcher is a single-host job scheduler distributed as a standalone Go binary named `dispatch`. It runs existing scripts and commands according to a YAML configuration, records job outcomes in SQLite, and sends Discord notifications. It adds execution history, overlap protection, retries, and dependency ordering to recurring tasks such as backups, data pipelines, and health checks.

## Goals

- Define recurring jobs and their dependencies in one readable configuration file.
- Make it easy to see which jobs ran, when they last succeeded, and which jobs failed.
- Prevent overlapping runs of the same job and support retries after failures.
- Keep installation and operation lightweight with a single static binary.

## Non-Goals

- Sub-second scheduling or real-time execution guarantees.
- Distributed job orchestration across multiple machines.
- Supervising long-running daemons or services.

## Requirements

- REQ-001: Provide a `dispatch` CLI that can create an example `Dispatcher.yaml` and validate the configuration.
- REQ-002: Execute configured scripts and commands on their schedules, with CLI controls to enable and disable scheduled execution.
- REQ-003: Support an ad hoc run of a named job without recording it in scheduled job history.
- REQ-004: Prevent concurrent scheduled executions of the same job.
- REQ-005: Honor configured job dependencies and retry policies.
- REQ-006: Persist scheduled job runs and their outcomes in SQLite and expose job status and last successful execution through the CLI.
- REQ-007: Send Discord notifications for job failures when notifications are configured.
- REQ-008: Distribute `dispatch` as a single static binary with no separate language runtime or database server.

## Architecture

The Go CLI reads job definitions and scheduling rules from `Dispatcher.yaml`. Cron invokes the dispatcher for scheduled execution. The dispatcher determines which jobs are due, applies dependency and overlap checks, and launches eligible commands. Local SQLite storage holds execution state and history for scheduling decisions and CLI status reporting. Discord notifications report failures when enabled.
