<!--
Keep it short. The point of these boxes is that someone reading the diff in
six months knows why it exists and what was actually verified.
-->

## What and why

<!-- One or two sentences. What changes, and what problem it solves. -->

## Verification

<!--
Which of these you actually ran, with results. "Should work" is not a result.
-->

- [ ] `cd web && npm run lint && npm test`
- [ ] `cd agent && gofmt -l . && go vet ./... && go test ./...`
- [ ] `cd web && npx playwright test`
- [ ] Tried it by hand (say how)

## Protocol

<!--
Delete this section if the wire format did not change.

If it did: the spec, the shared test vectors, and BOTH implementations have
to change together — a one-sided protocol change is not accepted
(CONTRIBUTING.md).
-->

- [ ] `protocol/README.md` or `protocol/signaling.md` updated
- [ ] Vectors added to `protocol/testvectors.json` / `signaling-testvectors.json`
- [ ] Go and TypeScript both green against the same vectors

## Notes

<!--
Anything the reviewer should know: what you did not do, what you are unsure
about, limits you accepted on purpose. Honest gaps here are worth more than a
clean-looking checklist.
-->
