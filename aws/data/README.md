# data

The EC2 database for the regions exported with `make catalog`. Same layout as
`google/data/`: `machine-types/<instance type>.json`, `disk-types/<volume type>.json`,
`topology/<instance type>/` for captures, `index.json`, plus `zones.json` mapping this
account's availability zone names to zone ids (names differ by account; ids do not).
