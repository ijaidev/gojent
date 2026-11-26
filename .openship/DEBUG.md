# Debug

Use when a test fails, a build breaks, or behavior is wrong.

## Loop

1. Reproduce with the smallest command. Capture exact output.
2. Read the error, the test, and the code path. Do not guess yet.
3. State one hypothesis and its prediction.
4. Run one experiment. Change one variable.
5. Record: confirmed | rejected | inconclusive.
6. Repeat ≤3 times, then stop and report.

## Minimize

- Reduce input, calls, and conditions until the failure is one assertion.
- Delete your recent changes; does the failure remain?
- Bisect commits, config, or data when the trigger is unclear.
- Compare a known-good path with the failing path; name the first difference.

## Fix

- Regression test first when possible: fail before, pass after.
- Fix the cause, not the symptom. No retries, sleeps, or broad rescues.
- Run the failing test, then the nearest suite. Do not run everything by reflex.
- Re-run the original repro at the end. Evidence, not inference.

## Verify

- Drive the real surface once: the CLI command, HTTP call, or UI path the change affects.
- Record the check and the observed result.
- Tests alone are `best-effort`; observed behavior is `verified`.

## Escalate

Stop when: no repro, 3 rejected hypotheses, missing access, or the fix needs a design decision.

Report: what you ruled out, the remaining hypothesis, and the exact command that shows the failure.
