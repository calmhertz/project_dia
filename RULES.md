# Aagasa Engineering Rules

These rules apply to the entire Aagasa project and all components: server, worker, client, deployment, tests, scripts, and supporting tools.

## 1. Non-Negotiable Rules

### 1.1 Security First

Security has the highest priority.

- Do not weaken authentication, authorization, credential handling, input validation, or access control for convenience.
- Never hard-code secrets, passwords, tokens, private keys, or production credentials.
- Treat all external input as untrusted.
- Follow the security requirements defined in the global and component specifications.
- Do not expose sensitive user, system, worker, server, or infrastructure information to users who are not authorized to see it.
- Preserve auditability for privileged and security-sensitive operations.
- Prefer established, well-understood security mechanisms over custom security implementations.

### 1.2 Reliability Second

The system must remain predictable and recoverable when components, networks, or external services fail.

- Handle expected failures explicitly.
- Do not assume that the network, server, worker, database, external TLE provider, hardware, or SatDump is always available.
- Make operations safe to retry where practical.
- Preserve durable state before reporting an operation as complete.
- Do not silently lose schedules, recordings, synchronization state, or important operational information.
- Follow the offline and recovery behavior defined by the global specification.
- Do not introduce behavior that can cause unsafe ground-station operation.

### 1.3 Keep the Implementation Simple

Use the simplest implementation that satisfies the specification.

- Do not add abstractions without a concrete requirement.
- Do not introduce frameworks, libraries, services, protocols, databases, queues, or design patterns merely because they are available.
- Prefer straightforward code over clever code.
- Prefer existing project dependencies and established project patterns.
- Avoid premature optimization.
- Avoid speculative features.
- Do not implement future-version functionality unless the current implementation requires a compatible foundation for it.

### 1.4 Reusability

After security and reliability, prefer reusable components where reuse is genuinely useful.

- Keep independent responsibilities separated.
- Avoid tightly coupling unrelated functionality.
- Reuse well-defined utilities instead of duplicating important logic.
- Do not create generic abstractions merely to satisfy an abstract idea of reusability.

### 1.5 Maintainability

Code must remain understandable to another developer.

- Keep modules focused.
- Keep functions reasonably small and single-purpose.
- Use consistent project conventions.
- Keep configuration explicit.
- Keep dependencies understandable.
- Avoid unnecessary indirection.
- Make failure behavior obvious.
- Prefer code that is easy to debug and operate.

### 1.6 Clear and Simple Identifiers

Use simple, descriptive identifiers.

- Variable names should describe what they contain.
- Function names should describe what they do.
- Type names should describe what they represent.
- Avoid unnecessary abbreviations.
- Avoid cryptic names such as `x`, `tmp2`, `foo`, or unexplained acronyms except for genuinely conventional names.

## 2. Logging and Output

### 2.1 Keep Logs Short

Do not produce unnecessarily long logs, `printf` output, or console output.

Logs should contain useful operational information rather than dumping large objects or excessive diagnostic text.

### 2.2 ASCII-Only Log and Print Output

All log messages and `printf`/console output must use ASCII characters only.

Do not place:

- emojis
- Unicode symbols
- decorative characters
- non-ASCII punctuation
- non-ASCII text

in logs or `printf` output.

This rule applies to application logs, worker logs, server logs, CLI output, scripts, and diagnostic output.

### 2.3 Do Not Log Sensitive Data

Never log:

- passwords
- access tokens
- refresh tokens
- API secrets
- private keys
- session credentials
- complete authentication headers
- other sensitive credentials

Be careful with user data, recording metadata, network information, and system configuration as well.

## 3. Comments and Documentation in Code

Keep code comments short and useful.

### Allowed

Use simple comments when they help organize or explain a non-obvious part of the implementation.

```text
# Sync worker state
```

### Avoid

Do not write long comments that restate the code or contain unnecessary implementation essays.

Do not use comments to compensate for confusing code. Prefer clearer code and identifiers.

Comments should explain **why** something non-obvious is necessary rather than narrating every line.

## 4. Code Quality

Every implementation must be:

- well organized
- consistently formatted
- modular
- readable
- testable
- secure
- robust
- straightforward

Follow the formatting and linting conventions of the language and existing project.

Do not leave temporary debugging code in completed work.

Remove unnecessary:

- debug prints
- dead code
- commented-out implementations
- temporary files
- unused dependencies
- unused configuration
- TODOs that are no longer relevant

## 5. Incremental Implementation

Aagasa must be implemented incrementally.

Do not attempt to implement an entire subsystem in one uncontrolled change.

For each feature:

1. Understand the relevant global and component specifications.
2. Identify the smallest useful implementation step.
3. Implement that step.
4. Format the code.
5. Run the relevant tests/checks.
6. Verify the behavior against the intended requirement.
7. Only then proceed to the next step.

Keep changes small enough that a failure can be easily isolated.

Do not combine unrelated features into one change.

## 6. Verification Before Progressing

After each meaningful implementation step, verify the result.

Depending on the component, this may include:

- unit tests
- integration tests
- API tests
- protocol tests
- database migration checks
- static analysis
- formatting
- linting
- build checks
- container build checks
- deployment validation
- hardware-interface tests where available

If the required verification cannot be performed, clearly state what could not be verified and why.

Do not claim a feature works merely because the code compiles.

## 7. Stop and Ask When Requirements Are Unclear

If there is genuine uncertainty about the intended behavior, **stop and ask before making the change**.

Do not silently choose a behavior when:

- two requirements conflict
- an important implementation detail is unspecified
- a hardware behavior is unclear
- a security decision is ambiguous
- a data-loss decision is ambiguous
- an API/protocol contract is unclear
- a change could affect existing behavior in a significant way

Do not invent requirements.

However, do not ask unnecessary questions when the specifications already provide a clear answer.

Use the existing global specification, component specification, implementation plan, reference code, and established project conventions before asking.

## 8. Reference Code

When reference code exists in `REFERENEC/`:

- inspect it before implementing the corresponding functionality
- reuse proven behavior where appropriate
- verify that the reference implementation actually satisfies the current specification
- improve or adapt it when necessary
- do not blindly copy it
- do not treat reference code as the final architecture

In particular, existing working hardware-interface code should be preferred over inventing an untested protocol implementation.

## 9. Do Not Over-Engineer

Do not introduce:

- unnecessary microservices
- unnecessary abstraction layers
- unnecessary message brokers
- unnecessary distributed state systems
- unnecessary generic hardware interfaces
- unnecessary caching
- unnecessary background workers
- unnecessary configuration systems

Every additional moving part increases operational and failure complexity.

Use the architecture explicitly defined by the Aagasa specifications.

## 10. Preserve Existing Contracts

Before modifying an existing implementation:

- understand its current behavior
- identify the relevant specification
- determine whether the behavior is intentional
- avoid breaking established APIs, protocols, database contracts, or deployment behavior without a deliberate reason

When a specification requires a breaking change, implement it deliberately and update all affected components.

## 11. Scope Control

Only implement what the current task requires.

Do not:

- redesign unrelated modules
- refactor large portions of the project without need
- add speculative functionality
- change technologies without a requirement
- modify unrelated files simply because they could be improved

If a necessary adjacent change is discovered, keep it minimal and explain why it is required.

## 12. Dependency Discipline

Before adding a dependency:

1. Check whether the existing project already provides the required capability.
2. Check whether the standard library or existing dependencies are sufficient.
3. Add a dependency only when it materially improves correctness, security, reliability, or maintainability.
4. Avoid dependencies that duplicate existing functionality.

Keep dependency count and operational complexity under control.

## 13. Error Handling

Errors must be explicit and actionable.

- Do not silently ignore important errors.
- Do not swallow exceptions/errors without a deliberate reason.
- Preserve useful error context.
- Do not expose internal secrets or sensitive implementation details through user-facing errors.
- Use appropriate error types/statuses for APIs and internal boundaries.
- Make retryable and non-retryable failures distinguishable where needed.

## 14. Data Integrity

Important state must not be partially or silently corrupted.

Pay particular attention to:

- schedules
- pass state
- approvals
- TLE data
- worker state
- recordings
- upload state
- synchronization state
- user roles
- configuration

Operations that may be retried must be designed so retries do not create duplicate or conflicting state where idempotency is required.

## 15. Hardware Safety

Ground-station hardware is operational equipment.

Hardware-related code must:

- validate commands and parameters
- use safe bounds
- handle communication failures
- stop or place hardware in the defined safe state when required
- avoid uncontrolled repeated commands
- follow the Yaesu G-550 and RTL-SDR behavior defined by the worker specification

Do not invent hardware behavior when the reference implementation or specification already defines it.

## 16. Configuration

Keep environment-specific configuration outside source code.

Use the project's defined:

- environment variables
- configuration files
- deployment configuration
- secrets mechanisms

Do not hard-code:

- hostnames
- credentials
- production ports unless explicitly part of the protocol
- machine-specific paths
- hardware-specific values that are intended to be configurable

Reasonable protocol constants may be defined in code when explicitly required.

## 17. API and Protocol Changes

When changing an API or protocol:

- update the authoritative contract
- update all affected implementations
- update tests
- preserve backward compatibility when required
- do not silently change field meaning
- keep protocol messages explicit and understandable

For gRPC contracts, treat `.proto` definitions as shared contracts between components.

## 18. Database Changes

Database changes must be deliberate.

- Use migrations.
- Do not manually modify production schemas as part of normal application behavior.
- Preserve data when changing schemas unless data removal is explicitly required.
- Add appropriate constraints for important invariants.
- Let the database enforce critical consistency rules where appropriate.

## 19. Testing Philosophy

Tests should verify behavior, not implementation trivia.

Prioritize tests for:

1. security
2. critical business rules
3. scheduling conflicts
4. synchronization
5. failure/recovery behavior
6. data integrity
7. hardware command safety
8. APIs and protocol contracts

Do not create large numbers of low-value tests simply to increase coverage.

## 20. Agent Workflow

When working as an implementation agent:

### Before changing code

- Read the relevant specification.
- Read the relevant implementation-plan phase.
- Inspect existing code.
- Inspect relevant reference code.
- Identify dependencies and affected components.
- Confirm the requested scope.

### During implementation

- Make the smallest reasonable change.
- Keep the implementation simple.
- Follow existing conventions.
- Do not jump ahead to later phases.
- Do not modify unrelated components.

### After implementation

- Format.
- Lint/static-check where applicable.
- Run relevant tests.
- Perform a focused verification.
- Review the diff for unnecessary changes.
- Report what was implemented and what was verified.

### If blocked

Stop and ask for clarification rather than guessing when the ambiguity could affect architecture, security, reliability, data integrity, or hardware behavior.

## 21. Priority Order

When making an engineering decision, use this priority order:

1. **Security**
2. **Reliability**
3. **Reusability**
4. **Maintainability**
5. **Simplicity**

If two requirements appear to conflict, prefer the higher-priority concern and ask for clarification when the conflict cannot be resolved from the specifications.

## 22. Final Rule

Do not optimize for writing the most code.

Optimize for producing the **smallest correct, secure, reliable, understandable, and verifiable implementation** that satisfies the current Aagasa specification and implementation phase.
