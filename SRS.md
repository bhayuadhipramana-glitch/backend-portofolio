# PRD: Portfolio Backend

Oct 3, 2026 · @Brian Arta Winata

## Summary

This product is a Go-based portfolio backend that proves its performance with numbers, not claims: p99 reads under 50 ms and 5,000 RPS on a single node, measured automatically in CI and displayed directly to visitors. This document only covers the backend, database, and infrastructure; the frontend is discussed separately.

| Decision | Choice | Reasoning |
| --- | --- | --- |
| Hosting | Hybrid: Proxmox (k3s) homelab as primary runtime, Azure for off-site backup | Homelab has no monthly fee and demonstrates infrastructure capabilities. $100 Azure credit is too small to run the entire stack 24/7. |
| Public access | Cloudflare Tunnel and CDN | No need for a public IP at home. A static frontend remains running when homelab is down. |
| SDLC | Hybrid: Full design up front, build incrementally per milestone, test at each milestone | Performance claims must be tested early, not later. |
| Architecture | Modular monolith plus one worker, Hexagonal pattern | Sufficient for target load, easy to work on alone, still scalable later. |
| Data | PostgreSQL 17, Valkey, NATS JetStream | Relational as source of truth, cache for read path, lightweight broker for event pipeline. |
| Key features | Real-time event pipeline with live feed via SSE | Generates real-world load so performance can be demonstrated. |

All options outside of hosting, SDLC, and UML use default answers from the Step 1 form. Options that still require confirmation are in the Open-ended Questions section.

## Goals and Non-Goals

The primary goal is one: a senior engineer who spends 60 seconds browsing this site or repo concludes that the owner is capable of designing a fast, observable, and operational system. The natural traffic of a portfolio is very small, so performance must be proven through a purposefully designed workload.

### Goals

| ID | Goal | Size |
| --- | --- | --- |
| G1 | Serve portfolio content (projects, résumés, articles, contacts) via a fast API | p99 read under 50ms at the origin |
| G2 | Run a real-time event pipeline that visitors can experiment with | Events appear in a live feed less than 2 seconds after being sent |
| G3 | Publish proof of performance | Load test reports, public dashboards, and flame graphs are available on the website |
| G4 | Entire infrastructure defined as code | Environment can be rebuilt from scratch in 1 hour |
| G5 | Run without a monthly cloud subscription fee | Azure expenses do not exceed student credit |

### Non-goals version 1

- Frontend and UI design.
- Microservices, multi-region, and full high availability. The system runs on a single physical machine.
- Batch processing, data warehousing, and orchestrators like Airflow.
- Multi-tenancy or user registration. Only one admin.
- Payment or monetization.

## Target users

There are three human actors and two system actors; the reviewing engineer is the one who ultimately influences design decisions.

| Actors | Who | Primary needs |
| --- | --- | --- |
| Visitor | Recruiter or hiring manager | Quickly view projects and resumes on any device, then contact the owner |
| Reviewing engineer | Senior engineer who assesses technical quality | View evidence: latency figures, architecture, dashboards, code, and load test results |
| Admin | Portfolio owner | Manage content, monitor systems, and run benchmarks |
| Load generator | Internal process | Send synthetic events to fuel the demo pipeline |
| CI/CD | GitHub Actions and Argo CD | Test, build images, and deploy without homelab login |

## Version 1 feature scope

Version 1 contains nine features; six are P0 priority and must be completed before release.

| ID | Feature | Description | Priority |
| --- | --- | --- | --- |
| F1 | Content API | Read endpoint for profiles, projects, articles, and résumés, with two-layer caching | P0 |
| F2 | Admin and authentication | GitHub OAuth login for a single admin, content CRUD, server-side sessions | P0 |
| F3 | Event ingest | Endpoint receives single or batch events, validates them, and publishes them to NATS JetStream | P0 |
| F4 | Aggregation worker | Idempotent consumer that writes raw and aggregated events per minute to PostgreSQL | P0 |
| F5 | Live feed | SSE stream containing the latest throughput, latency, and aggregates | P0 |
| F6 | Observability | Traces, metrics, and logs via OpenTelemetry; public read-only Grafana dashboard | P0 |
| F7 | Demo burst | Visitors trigger controlled load bursts and see the impact in the live feed | P1 |
| F8 | Benchmark page | Historical k6 results from CI and flame graph pprof | P1 |
| F9 | Contact form | Visitor messages are stored and forwarded via outbox; rate-limited | P1 |

Delayed to version 2: CDC with Debezium, ClickHouse for analytics, full-text search outside of PostgreSQL, and automatic failover to Azure.

## Success Metrics and SLOs

The term "zero-bottleneck" translates to eight targets.