# 0001: Use Go for the backend

- Status: accepted
- Date: 2026-09-11

## Why

The backend needs to search several nodes at once, stop work when a request ends, and run as small
services.

## Choice

Use Go for the backend services and search code. A separate service can run a larger search model if
one is added later.

## Result

Networking, saved data, search, tests, and benchmarks use one language. Memory use still needs to be
measured as indexes grow.
