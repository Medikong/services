# Agent Guidelines for Python Code Quality

This document provides guidelines for maintaining high-quality Python code. These rules MUST be followed by all AI coding agents and contributors.

## Technology Stack

| Area | Default candidates |
| --- | --- |
| Backend | Python, FastAPI, Starlette, Pydantic v2, JWT |
| ASGI Runtime | Uvicorn, Gunicorn |
| Auth & Security | PyJWT, python-jose, passlib, pwdlib, bcrypt, argon2-cffi |
| Data & Messaging | PostgreSQL, MongoDB, Kafka, SQLAlchemy, Alembic, asyncpg, psycopg, PyMongo |
| Cache & Queue | redis-py, Celery, Dramatiq, arq |
| Messaging Client | aiokafka, confluent-kafka |
| Config | pydantic-settings, python-dotenv |
| Platform | Docker, Kubernetes, Istio |
| CI/CD & IaC | GitHub Actions, Helm, Argo CD, Terraform, AWS, Amazon ECR |
| Logging | structlog, standard logging |
| Observability | OpenTelemetry, prometheus-client, Prometheus, Alertmanager, Grafana, Loki, Tempo |
| Quality & Test | pytest, pytest-asyncio, httpx, respx, testcontainers-python, factory-boy, k6, Postman, Newman, Trivy |
| Static Analysis | ruff, mypy, pyright, bandit, pip-audit |
| Docs & API | OpenAPI, Swagger UI, Redoc |

## Your Core Principles

All code you write MUST be fully optimized.

"Fully optimized" includes:

- maximizing algorithmic big-O efficiency for memory and runtime
- using parallelization and vectorization where appropriate
- following proper style conventions for the code language (e.g. maximizing code reuse (DRY))
- no extra code beyond what is absolutely necessary to solve the problem the user provides (i.e. no technical debt)
  - If a Python library can be imported to significantly reduce the amount of new code required to implement a function at optimal performance, and the library itself is small and does not have much overhead, ALWAYS use the library instead.

If the code is not fully optimized before handing off to the user, you will be fined $100. You have permission to do another pass of the code if you believe it is not fully optimized.

## Ponytail Implementation Principles

Use the `$ponytail` skill by default when deciding whether a wrapper,
abstraction, dependency, or boilerplate is necessary. Default intensity is
`full`.

- First ask whether the code needs to exist. Do not add speculative extension
  points, single-implementation protocols, factories for one product, or config
  for values that do not change.
- Reuse existing project helpers, types, and patterns first. Then check the
  standard library, native platform feature, and already-installed dependency,
  in that order.
- Do not create wrappers that only rename a standard library, framework, SDK,
  or third-party API.
- Wrap only when there is a structural reason. Examples: multiple real
  implementations exist now, a test seam is needed now, project runtime policy
  must be centralized, or a domain boundary needs protection.
- Use OOP only when it pays for itself. Before adding a class, base class,
  decorator, manager, provider, or adapter, check whether one function,
  dataclass, or concrete type is enough.
- Prefer deletion over addition. Keep file count and diff size small after
  tracing the real call path.
- Mark deliberate shortcuts with a `# ponytail:` comment that names the ceiling
  and the upgrade trigger.
- Never simplify away validation at trust boundaries, error handling that
  prevents data loss, security, accessibility, or explicitly requested
  behavior.

## Domain-Based Package Structure

- Prefer domain-based packages over file-type packages such as `models/`, `services/`, `repositories/`, or `crud/`.
- Each domain package owns its models, schemas, service logic, repository protocol, validation, errors, and persistence adapters.
- `app/http` owns FastAPI routers, dependencies, and exception handlers only. Do not put business rules in routers.
- `app/main.py` or the app factory is the composition root. It wires dependencies but does not contain domain logic.
- Runtime concerns such as database sessions, MongoDB clients, Kafka producers, and Redis clients belong in `app/platform` or an equivalent runtime package.
- Domain-specific SQL, MongoDB, Redis, or Kafka code belongs in the domain package. Do not collect all persistence code in a global `store/` or `repositories/` package.
- Split adapters into `<domain>/postgres.py`, `<domain>/mongodb.py`, or `<domain>/kafka.py` when the domain grows enough to need separate files.

```text
app/
├── main.py
├── config.py
├── http/
│   ├── routes.py
│   ├── dependencies.py
│   └── errors.py
├── platform/
│   ├── postgres.py
│   ├── mongodb.py
│   └── kafka.py
├── account/
│   ├── models.py
│   ├── schemas.py
│   ├── service.py
│   ├── repository.py
│   ├── postgres.py
│   ├── validation.py
│   └── errors.py
└── session/
    ├── models.py
    ├── service.py
    ├── repository.py
    └── postgres.py
```

## Preferred Tools

- Use `uv` for Python package management and to create a `.venv` if it is not present.
- Ensure `ipykernel` and `ipywidgets` is installed in `.venv` for Jupyter Notebook compatability. This should not be in package requirements.
- Use `tqdm` to track long-running loops within Jupyter Notebooks. The `description` of the progress bar should be contextually sensitive.
- Use `orjson` for JSON loading/dumping.
- When reporting error to the console, use `logger.error` instead of `print`.
- If the project involves the creation of images (e.g. PNG/WEBP), you have permission to use the Read tool to verify the rendered images fit the user and application requirements.
- For data science:
  - **ALWAYS** use `polars` instead of `pandas` for data frame manipulation.
  - If a `polars` dataframe will be printed, **NEVER** simultaneously print the number of entries in the dataframe nor the schema as it is redundant.
  - **NEVER** ingest more than 10 rows of a data frame at a time. Only analyze subsets of code to avoid overloading your memory context.
- For creating databases:
  - Do not denormalized unless explicitly prompted to do so.
  - Always use the most appropriate datatype, such as `DATETIME/TIMESTAMP` for datetime-related fields.
  - Use `ARRAY` datatypes for nested fields. **NEVER** save as `TEXT/STRING`.
- In Jupyter Notebooks, DataFrame objects within conditional blocks should be explicitly `print()` as they will not be printed automatically.

## Code Style and Formatting

- **MUST** use meaningful, descriptive variable and function names
- **MUST** follow PEP 8 style guidelines
- **MUST** use 4 spaces for indentation (never tabs)
- **NEVER** use emoji, or unicode that emulates emoji (e.g. ✓, ✗). The only exception is when writing tests and testing the impact of multibyte characters.
- Use snake_case for functions/variables, PascalCase for classes, UPPER_CASE for constants
- Limit line length to 88 characters (ruff formatter standard)
- **MUST** avoid including redundant comments which are tautological or self-demonstating (e.g. cases where it is easily parsable what the code does at a glance so the comment does)
- **MUST** avoid including comments which leak what this file contains, or leak the original user prompt, ESPECIALLY if it's irrelevant to the output code.

## Documentation

- **MUST** include docstrings for all public functions, classes, and methods
- **MUST** document function parameters, return values, and exceptions raised
- Keep comments up-to-date with code changes
- Include examples in docstrings for complex functions

Example docstring:

```python
def calculate_total(items: list[dict], tax_rate: float = 0.0) -> float:
    """Calculate the total cost of items including tax.

    Args:
        items: List of item dictionaries with 'price' keys
        tax_rate: Tax rate as decimal (e.g., 0.08 for 8%)

    Returns:
        Total cost including tax

    Raises:
        ValueError: If items is empty or tax_rate is negative
    """
```

## Type Hints

- **MUST** use type hints for all function signatures (parameters and return values)
- **NEVER** use `Any` type unless absolutely necessary
- **MUST** run mypy and resolve all type errors
- Use `Optional[T]` or `T | None` for nullable types

## Error Handling

- **NEVER** silently swallow exceptions without logging
- **MUST** never use bare `except:` clauses
- **MUST** catch specific exceptions rather than broad exception types
- **MUST** use context managers (`with` statements) for resource cleanup
- Provide meaningful error messages

## Function Design

- **MUST** keep functions focused on a single responsibility
- **NEVER** use mutable objects (lists, dicts) as default argument values
- Limit function parameters to 5 or fewer
- Return early to reduce nesting

## Class Design

- **MUST** keep classes focused on a single responsibility
- **MUST** keep `__init__` simple; avoid complex logic
- Use dataclasses for simple data containers
- Prefer composition over inheritance
- Avoid creating additional class functions if they are not necessary
- Use `@property` for computed attributes

## Testing

- **MUST** write unit tests for all new functions and classes
- **MUST** mock external dependencies (APIs, databases, file systems)
- **MUST** use pytest as the testing framework
- **NEVER** run tests you generate without first saving them as their own discrete file
- **NEVER** delete files created as a part of testing.
- Ensure the folder used for test outputs is present in `.gitignore`
- Follow the Arrange-Act-Assert pattern
- Do not commit commented-out tests

## Imports and Dependencies

- **MUST** avoid wildcard imports (`from module import *`)
- **MUST** document dependencies in `pyproject.toml`
- Use `uv` for fast package management and dependency resolution
- Organize imports: standard library, third-party, local imports
- Use `isort` to automate import formatting

## Python Best Practices

- **NEVER** use mutable default arguments
- **MUST** use context managers (`with` statement) for file/resource management
- **MUST** use `is` for comparing with `None`, `True`, `False`
- **MUST** use f-strings for string formatting
- Use list comprehensions and generator expressions
- Use `enumerate()` instead of manual counter variables

## Benchmarking and Optimization

- **NEVER** run benchmarks in parallel, as the benchmarks will compete for resources and the results will be invalid
- **NEVER** game the benchmarks. Do not manipulate the benchmarks themselves to satisfy any required performance constraints
- If benchmarking against another crate or library, ensure the benchmarks are apples-to-apples comparisons
- Ensure benchmark tests are independent. If the tests are dependent due to a feature (e.g. caching), ensure the feature is disabled

## Security

- **NEVER** store secrets, API keys, or passwords in code. Only store them in `.env`
  - Ensure `.env` is declared in `.gitignore`.
  - **NEVER** print or log URLs to console if they contain an API key
- **MUST** use environment variables for sensitive configuration
- **NEVER** log sensitive information (passwords, tokens, PII)

## Version Control

- **MUST** write clear, descriptive commit messages
- **NEVER** commit commented-out code; delete it
- **NEVER** commit debug print statements or breakpoints
- **NEVER** commit credentials or sensitive data

## Tools

- **MUST** use Ruff for code formatting and linting (replaces Black, isort, flake8)
- **MUST** use mypy for static type checking
- Use `uv` for package management (faster alternative to pip)
- Use pytest for testing

## Before Committing

- [ ] All tests pass
- [ ] Type checking passes (mypy)
- [ ] Code formatter and linter pass (Ruff)
- [ ] All functions have docstrings and type hints
- [ ] No commented-out code or debug statements
- [ ] No hardcoded credentials

---

**Remember:** Prioritize clarity and maintainability over cleverness. This is your core directive.
