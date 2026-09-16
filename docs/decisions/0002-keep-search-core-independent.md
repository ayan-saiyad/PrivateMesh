# 0002: Keep search code independent

- Status: accepted
- Date: 2026-09-11

## Why

Search and ranking should be easy to test without starting the full app.

## Choice

Keep the main search code separate from HTTP, gRPC, login, databases, and outside model services.
Small adapter packages connect those parts.

## Result

Search tests and benchmarks can run in memory. Network and storage code can change without rewriting
the ranking code.
