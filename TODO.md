# TODO

## Group clearing and verification

- [x] Handle an entirely expired group: send `{"member":[]}` instead of
  `{"member":null}` when no members remain.
- [x] Read group membership after PUT and stop before DELETE if any address
  scheduled for deletion is still a member. HTTP 200 alone is insufficient.
- [x] Preserve FortiGate's numeric `error` code and useful error details in
  failure reports.
- [x] Add regression tests for an entirely expired group and a PUT that
  returns HTTP 200 without removing the members.

Observed on a live device: 600 expired entries, all 600 deletions rejected
with HTTP 500, and all members still present. The null payload is the leading
suspect; the FortiGate error code is needed to confirm the deletion reason.
The count of 600 describes this run, not a universal device capacity or cap.
The fix is implemented and covered by local API tests; live-device
verification remains to be done.

## Slow FortiGate writes

- [x] Give group PUT and config save POST a configurable timeout independent
  of the 20-second read/delete timeout.
- [x] On a timed-out group PUT, read the group back and proceed only if the
  expired members are confirmed absent; never retry the PUT blindly.
- [ ] Verify suitable `-write-timeout` and read-back behavior on the live
  FortiGate. A timed-out save POST cannot prove persistence by its response.

## Batch mode — discuss before implementing

- [ ] Determine whether processing a few addresses at a time is useful.
  Distinguish sequential chunks, limited parallel DELETE requests, and an
  actual bulk API operation; verify what the target FortiOS version supports.
- [ ] Check whether FortiOS supports safe incremental group-member removal.
  Chunking DELETE alone does not shorten the full-group PUT that timed out.
- [ ] Decide batch size, request rate, and how to handle partial failures and
  concurrent changes to group membership.
- [ ] Do not hardcode a 600-entry cap. Determine relevant limits for the
  device model and FortiOS version, and distinguish object/group capacity
  from REST API pagination. Keep batch size configurable and independent of
  device capacity; cover different group sizes in tests.
- [ ] Evaluate run duration against `cfg-save=revert` timeouts. Keep explicit
  configuration saving conditional on overall cleanup success; do not commit
  partial batches implicitly.
- [x] Fix group clearing and membership verification before benchmarking:
  batching alone does not resolve the current HTTP 500 failure.

## Quiet mode for schedulers

- [ ] Define an optional `-quiet` mode without the banner, table, or normal
  success output on stdout.
- [ ] Decide how warnings should be handled. Keep actionable failures on
  stderr and preserve nonzero exit codes so schedulers can detect failures.
- [ ] Expose the mode in the PowerShell wrapper and document Task Scheduler
  usage. Account for save failures, whose details currently live in the
  buffered report.
