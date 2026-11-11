#!/usr/bin/env bash
while true; do
    python3 harvest.py search --max-queries 100
    python3 harvest.py scan --workers 4
done
