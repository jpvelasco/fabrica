# Horde jobs: stream, template, and targets

`fabrica horde submit` creates a job with `POST /api/v1/jobs`. Horde 5.8 runs a
job from a **stream** with a **job template**, so both IDs are required. They
must already exist in the coordinator's `globals.json`.

```yaml
horde:
  streamId: //UE5/Main        # --stream
  templateId: build-template  # --template; a template the stream offers
  change: 0                   # --change; 0 lets Horde pick the latest change
```

Submit fails before any state or network access when `streamId` or
`templateId` is empty, including under `--dry-run`. Fabrica does not check the
IDs against the coordinator. Horde rejects an unknown stream with HTTP 404 and a
template the stream does not offer with HTTP 400; submit prints the Horde
message with a hint naming the key to fix.

## What a submit sends

```json
{"streamId":"//UE5/Main","templateId":"build-template","name":"Nightly","commitId":"4242","targets":["Cook"]}
```

- `commitId` is the changelist as a string; it is omitted when `change` is 0.
- `name` is `--name`, or the BuildGraph agent name when a file is given.
- `targets` is sent only when there is a target (from the BuildGraph file, or
  `--target`). Otherwise Horde runs the template's default target.

Fabrica never sends `arguments`. Horde uses it to replace the template's whole
argument list, which would drop the template's `-Script=`. `targets` replaces
only the `-Target=` arguments.

## BuildGraph files are local-only

`fabrica horde submit [buildgraph.xml]` parses the file to resolve the target
(the first `<Aggregate>`, or the first `<Node>` of the first `<Agent>`;
`--target` overrides it). The file is **never uploaded**: Horde runs the script
the stream has synced, through the template.

`-Script` belongs to the Horde template and is not settable from Fabrica.
Appending it through `additionalArguments` would not reliably override the
template's value.

```bash
fabrica horde submit --dry-run   # prints stream, template, target, and the request JSON
fabrica horde submit --wait      # a wrong stream or template fails with a hint
```

`--wait` exits non-zero when any step fails or any batch errors. Horde reports
`Complete` when a job stops, even if its steps failed, so fabrica reads each
batch and step before reporting success. A step that completed with warnings
still succeeds; its warning is printed. The error names the job, up to 10
failing items, and the dashboard link `<coordinator>/job/<id>`. For
`NoAgentsInPool` or `NoAgentsOnline`, check `fabrica horde agents` and approve
pending agents with `fabrica horde agents approve`.

A 401 or 403 means the `fabrica-ci` service account lacks job permission.
