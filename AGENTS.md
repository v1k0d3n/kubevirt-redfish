# Redfish emulator for KubeVirt

You are building a cloud native service for kubernetes and OpenShift
that exposes a Redfish http endpoint and lets the authorized users
control specific VirtualMachines in specific configured namespaces.

Namespaces are exposed as Redfish Chassis, VMs are exposed as Systems.

## Reliability requirements

The redfish logic should use cluster objects themselves for storing
long term goals. Either labels or annotations should represent the
future or status markers.

Any long term process monitored by the Redfish service should be
able to continue, reconcile or be monitored even when the Redfish
service restarts or is replaced by a new instance.

This means you should avoid starting long running threads or goroutines
and prefer making notes on the affected objects (like VMs, VMIs or Pods)
that can then be monitored using kubernetes watch mechanisms.

## Security and privacy requirements

No passwords should be logged to console or log files.

Take extra care when openshift side information is exposed, users must
not be able to see or modify objects from namespaces they are not
allowed to access. Only let users access or modify objects from
namespaces they are explicitly allowed to work with via configuration.

# Development practices

- The programming language is golang.
- Before writing the implementation write a test that describes the functionality
  you are trying to implement.
- Use mock clients for network endpoint calls and for representing time
- Then implement the logic in a way that can be tested both locally with no network,
  possibly passing mocks as arguments or by splitting the login into pure functions.
- Use an subagent with the cheapest model like Haiku for analysing logs from cli
  operations like compilation or test execution. Only return the result and information
  needed to resolve the issue to the main agent and its context.
- Always make sure the tests pass.
- Always reformat the code using go fmt.
- When adding new dependencies, use the newest version possible unless told otherwise.
- Avoid meaningless code comments that only explain what the code already shows.
  You can document concepts and reasons for bigger code blocks or for logic that
  is distributed across multiple code paragraphs. In other words - do not document
  what each single line of code does, document the higher level ideas.
