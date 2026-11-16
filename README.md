# retainer

Memory-augmenting proxy in front of [rooter](https://github.com/initialed85/rooter).

Rooter routes; retainer *retains*. It sits between a client (e.g. pi) and rooter
and, for each chat request:

1. Asks laya-router (systemone) whether the latest user prompt is worth a
   memory search at all.
2. Asks local gemma (sentiment-analyzer endpoint) to turn the prompt into a
   search query.
3. Searches graphiti for facts.
4. Asks laya-router whether the facts are relevant enough to inject.
5. Appends a caveated, untrusted `<retrieved_context>` block to the user
   message, then forwards the request to rooter.

Retrieved context is cached per (session, prompt), so when a client replays
its history the previously injected context is replayed verbatim and the
upstream KV-cache prefix stays stable.

The point of the exercise: make a smaller model go further. Memory that the
model would otherwise have to be re-told every session is fetched instead,
and the laya gates keep that fetch to the turns that need it. The same
seam (rewrite the request before forwarding) is where model switching -
cheap model for easy turns, expensive for hard ones - is meant to land
next.

## Layout

- `cmd/retainer` - env config, wiring, server
- `pkg/proxy` - front-door key check, health, session detection, upstream
  forwarding, streaming
- `pkg/memory` - retrieval pipeline, per-(session,prompt) context cache,
  request rewriting, graphiti + gemma clients
- `pkg/laya` - laya-router systemone decision client

## Run

```shell
find cmd pkg | entr -n -r -cc -s "TOKEN='sk-rooter-...' LAYA_API_KEY='...' go run ./cmd/retainer"
```

## Env vars

| var | default | purpose |
|-----|---------|---------|
| `TOKEN` | (required) | rooter bearer token (outbound) |
| `FRONT_DOOR_KEY` | (none) | inbound bearer key; unset leaves the proxy open |
| `LAYA_API_KEY` | (unset) | laya-router decisions; unset leaves both gates open |
| `LAYA_MODEL` | `Laya-Q8_0` | laya model |
| `SESSION_ID` | (none) | fallback session id when the client sends no session header |
| `ADDR` | `0.0.0.0:8088` | listen address |
| `UPSTREAM` | `https://rooter.dev.initialed85.cc` | model router |
| `LAYA_URL` | `https://laya-router.dev.initialed85.cc` | decision endpoint |
| `TERMS_URL` | `https://sentiment-analyzer.dev.initialed85.cc/v1` | search-term model (OpenAI-compatible base URL) |
| `TERMS_MODEL` | (none) | optional model override for search terms |
| `STORE_URL` | `https://graph.dev.initialed85.cc/graphiti/search` | graphiti search endpoint (full URL; in-cluster it is `http://memory-graph.memory.svc.cluster.local:9999/search` because the public ingress strips the `/graphiti` prefix) |

Health: `GET /healthz` answers without a key (kubelet probes).

## Tests

```shell
go test ./...
```

## Deploy

`https://retainer.dev.initialed85.cc` - dev cluster, key-gated like rooter.
Manifests live in home-ops `clusters/dev/applications/retainer/` (Argo app
`retainer.yaml`); CI (`.woodpecker/build.yaml`) publishes
`docker.io/initialed85/retainer:<sha>` and stamps the kustomize tag. The
front-door key and the outbound rooter/laya tokens live in Secrets in the
`retainer` namespace, not in git.

A client points at `https://retainer.dev.initialed85.cc/v1` with
`Authorization: Bearer <front-door key>`; everything else (session headers,
streaming, models) behaves like rooter.

## Next steps (roadmap)

- On response: decide (laya, or always) whether to extract memories from the
  assistant response, and feed the response into graphiti - queued somewhere
  so the session stays responsive.
- Track user sentiment across [response-1, prompt] and use it in routing
  decisions.
- Track user goal per turn.
- Use all of the above to route models: drop to a cheaper one when the task
  is easy, shift to an expensive one when it is hard.
- Consider persisting session history / the context cache to a DB so the
  cache survives process restarts and is a source of truth for decisions.