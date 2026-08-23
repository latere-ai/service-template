# Bootstrap resources

The release pipeline never reads this directory. Everything it applies must be
idempotent, because it applies on every release, and the resources here are
one-time or immutable: namespaces, cluster-scoped grants, and anything whose
re-application would fight another controller.

Apply them by hand and record what you applied:

```sh
kubectl apply -f deploy/bootstrap/namespace.yaml
```

A resource that belongs to a release belongs in `deploy/base/` with a target
overlay, not here.
