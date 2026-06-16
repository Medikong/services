from observability.config import ObservabilityConfig


_profiling_configured = False


def configure_process_profiling(config: ObservabilityConfig) -> bool:
    """프로세스 단위 Pyroscope profiler를 한 번만 설정한다."""
    global _profiling_configured

    profiling = config.profiling
    if _profiling_configured or not profiling.enabled:
        return False
    if not profiling.server_address:
        raise ValueError("PYROSCOPE_SERVER_ADDRESS is required when PYROSCOPE_ENABLED=true")

    import pyroscope

    options: dict[str, object] = {
        "application_name": profiling.application_name or config.service_name,
        "server_address": profiling.server_address,
        "sample_rate": profiling.sample_rate,
        "oncpu": profiling.oncpu,
        "gil_only": profiling.gil_only,
        "tags": dict(profiling.tags or {}),
    }
    if profiling.basic_auth_username:
        options["basic_auth_username"] = profiling.basic_auth_username
    if profiling.basic_auth_password:
        options["basic_auth_password"] = profiling.basic_auth_password
    if profiling.tenant_id:
        options["tenant_id"] = profiling.tenant_id

    pyroscope.configure(**options)
    _profiling_configured = True
    return True
