# KubeAdmiral Code Style

## Linting

KubeAdmiral uses [golangci-lint](https://golangci-lint.run/usage/install/) for linting. All code should adhere to the rules specified in the [configuration file](../.golangci.yml).

In order to check if your code adheres to our rules, you may use golangci-lint. First, ensure that you have golangci-lint installed. Then, run the following command:

```console
$ golangci-lint run --timeout=2m --max-issues-per-linter=10 --go=1.19
```

## Logging Conventions

Logs play an important role when troubleshooting issues. Thus, it is also important to ensure that our logs are readable, navigable and doesn't devolve into noise.

In order to achieve this, KubeAdmiral follows a set of logging conventions described below.

1. Use of [klog](https://pkg.go.dev/k8s.io/klog/v2) is **REQUIRED**. Klog is the de facto logging package for Kubernetes development.

2. Use contextual logging whenever possible. `klog.FromContext` **MUST** be used to obtain a logger whenever there is a `Context` available.

3. Consequently, top-level controllers or reconciliation workers **MUST** inject a logger into its `Context` using `klog.NewContext` before passing it to any nested calls. The injected logger **MUST** have the following values:

    1. `controller`: indicating the name of the controller
    2. `object`: indicating the qualified name of the object being reconciled
    3. `ftc` (if applicable): indicating the name of the controller's corresponding ftc

4. Log messages **MUST** have the following format:

    1. **MUST** begin with a capital letter (with the exception of identifiers that conventionally begin with a lowercase)
    2. The ending period is **MUST** be omitted

5. Use structured logging fields to record variable arguments is **REQUIRED** (as opposed to using format strings).

6. Kebab case **MUST** be used for keys of structured logging fields.

7. Use the appropriate log level based on the following rules:

    1. 0: reserved for errors, warnings and controller lifecycle logs
    2. 1: document "what happened" by describing the actions taken by a controller to reconcile an object at a high level (this **MUST** not include any NOOPs)
    3. 2: document "how it happened" by describing the reconcile loop in more detail (e.g. trigger events, individual scheduling framework stages, individual steps of cluster joining, updating annotations) (this still **MUST** not include any NOOPs)
    4. 3: any additional reconciliation logs that do not belong to level 0, 1 or 2
    5. 4: logs for non-controller code such as utility packages
    6. &ge;=5: can be used for debugging purposes, but **MUST** be removed before committing

8. Log levels **MUST** only be set at the log call site, to prevent unexpected log levels due to addition.

