# AWS captures: cost estimate and guardrails

Status (2026-11-08): done. The runner (topology-agent's `aws` provider,
`aws/capture/run-plan.sh`, `make plan-topology`, `make capture-all`) ran the full plan at
`PRICE_CAP=0` overnight: 143 of 146 series captured, 139 on the first run and three one
size down afterwards (g6.24xlarge, r8id.48xlarge, vt1.6xlarge); g6e, g7e and p4de had no
capacity in any zone on four attempts spread over three hours and are left out. The
longest instance lived 12 minutes; the estimated EC2 cost from the log and the price
snapshot is $104, against the $162 estimate. No instance or volume was left behind: the
postflight check passed and `describe-instances` shows only terminated instances. The
guardrails fired once for real: the first t3.2xlarge attempt could not be reached over
SSH and was terminated by the runner; its scheduled self-shutdown was confirmed before
that. A capture that fails joins a retry set for the later passes; the run does not stop
on failures.

## The plan

One hwloc capture per series: the largest size the account's On-Demand quota allows,
Spot where the type has the usage class and the Spot quota covers it. From the us-east-1
catalog and quota snapshot in `data/` and `projects/<account>/quota.json`:

| Plan | Captures | Spot-eligible | With accelerators |
|---|---|---|---|
| VM sizes (default) | 146 | 136 | 21 (p, g, trn, inf, dl, f, vt) |
| With bare metal (`METAL=1`) | 238 | 227 | 21 |

Each capture is one instance for a few minutes: boot, run hwloc, copy the XML out,
terminate. Google captures took two to five minutes each.

## Cost, EC2 only

Prices on 2026-11-07 from the pricing API (Linux, shared tenancy, On-Demand) and the
current lowest Spot price in the region. No EBS, data transfer or NAT included; a
capture uses a root volume for minutes and moves kilobytes.

VM plan, 146 captures:

| Minutes per capture | Spot where possible | All On-Demand |
|---|---|---|
| 10 | $162 | $370 |
| 20 | $324 | $741 |
| 30 (the hard cap) | $486 | $1,111 |

Ten types carry most of it. Per hour, Spot or On-Demand where there is no Spot:

| Type | $/h | Note |
|---|---|---|
| u7i-8tb.112xlarge | 83.72 | On-Demand only |
| u7i-6tb.112xlarge | 62.79 | On-Demand only |
| u-6tb1.112xlarge | 54.60 | On-Demand only |
| p6-b200.48xlarge | 82.46 Spot / 113.93 | |
| p6-b300.48xlarge | 76.77 Spot / 142.42 | |
| p5en.48xlarge | 38.62 Spot / 63.30 | |
| g7e.48xlarge | 33.14 | On-Demand only |
| g6e.48xlarge | 30.13 | On-Demand only |
| g7.48xlarge | 28.51 | On-Demand only |
| u-3tb1.56xlarge | 27.30 | On-Demand only |

The median planned type costs $2.45 an hour. With an hourly price ceiling:

| `PRICE_CAP` | Captures | 10 min each | Worst case at the 30-min cap | Left out |
|---|---|---|---|---|
| $10/h | 126 | $53 | $158 | the ten above plus p4d, p4de, p5, trn1n, m8ib/m8in, r8ib/r8idn/r8in, g6 |
| $25/h | 136 | $76 | $227 | the ten above |
| $50/h | 141 | $102 | $306 | p6-b200, p6-b300, u-6tb1, u7i-6tb, u7i-8tb |
| none | 146 | $162 | $486 | |

Recommendation: run at `PRICE_CAP=25` first, about $80 expected and $230 bounded.
Decide the ten expensive types one by one afterwards; the three `u7i`/`u-6tb1` memory
types and the two `p6` types alone are $360 an hour between them.

What the Google incident would cost here: one a3-class node left for 13 hours was about
$690. The same mistake on p6-b300 On-Demand is $1,850; on u7i-8tb $1,090. The point of
the guardrails is that no instance can exist for 13 hours.

## Why this cannot repeat the Google failure

What went wrong on 2026-11-06, precisely: GKE put a node pool between us and the
machine. The pool create timed out on our side but kept running on Google's; the
provider's delete of the pool failed because the create was still in progress and the
failure was ignored; the script's single cluster delete failed because the cluster was
busy; nothing re-checked afterwards; the node came up later into a pool nobody was
watching and ran idle for 13 hours. Every link in that chain was orchestration that had
to succeed for the machine to go away.

On AWS the machine's lifetime does not depend on our orchestration succeeding:

1. **Lifetime by construction, on the instance itself.** Every capture is launched with
   `--instance-initiated-shutdown-behavior terminate` and user data that runs
   `shutdown -h +30` before anything else. If the orchestrator dies, the network drops,
   the capture hangs, or the account is closed from our laptop, the instance shuts
   itself down at 30 minutes and EC2 terminates it. Spot requests are `one-time`, never
   `persistent`, so a terminated instance is not replaced. This is the step GKE node
   pools had no equivalent of, and the reason the Google run could orphan a node at all.
   Worst case per capture is therefore 30 minutes of one instance, which is the
   "30 (the hard cap)" column above, not 13 hours.
2. **No indirection.** `run-instances` directly, no cluster, node pool, auto scaling
   group or launch template that could recreate capacity after we delete the instance.
   The one object we create is the one we terminate.
3. **One at a time.** The runner launches the next capture only after
   `describe-instances` shows the previous one terminated, or its 30 minutes are up and
   it is confirmed gone. There is never more than one capture instance in existence,
   so "everything left running 24 h" ($23,000) is not a reachable state.
4. **Preflight and postflight by tag.** Every instance carries
   `topology-agent=capture` and a run id. The script refuses to start if any instance
   with that tag exists (FORCE=1 overrides), and ends by listing them again; anything
   left means a non-zero exit, the instance ids and the exact terminate command printed,
   and a `terminate-instances` attempt with the result shown, not swallowed.
5. **Price ceiling and estimate up front.** The plan carries each type's hourly price
   from the pricing API; `PRICE_CAP` (dollars per hour) refuses types above it unless
   named in `ALLOW_EXPENSIVE`; the runner prints the expected and the 30-minute-capped
   total and waits for confirmation before the first launch.
6. **An AWS Budget as the backstop we do not control.** A monthly budget with an alert
   at a chosen dollar amount, created once in the console or with
   `aws budgets create-budget`, so a surprise is an email within hours, not a bill at
   month end. Billing data lags up to a day, so this catches the second mistake, not the
   first; items 1 to 3 are what prevent the first.
7. **Dry run first.** `DRY_RUN=1` prints every `run-instances` command with its price
   and the cap, launches nothing. The first real run is a single cheap type
   (`t3.2xlarge`, $0.07 an hour) to prove the self-termination fires, watched end to
   end, before the plan runs.

What this does not protect against: a type whose Spot price is far above the lowest
zone's (the runner passes a Spot max price equal to the On-Demand price, so it cannot
exceed the On-Demand column), and EBS or snapshot leftovers (root volumes are
`DeleteOnTermination`, and the postflight lists volumes and snapshots with the tag too).

## Order of work

1. The runner (`aws/capture/run.sh` and the `aws` provider in topology-agent) with items
   1 to 5 and 7 built in, and a test that launches nothing but checks every command
   line carries the shutdown behavior, the user-data timer and the tag.
2. The single-type proof run, watched.
3. The plan at `PRICE_CAP=25`.
4. The ten expensive types, each a separate decision.
