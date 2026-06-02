import os

from server.observability import ObservabilityConfig, observability_config_from_env


class Settings:
    service_name = "auth-service"
    database_url = os.getenv("DATABASE_URL", "sqlite:///./auth_service.db")
    token_ttl_seconds = int(os.getenv("AUTH_TOKEN_TTL_SECONDS", "900"))
    refresh_token_ttl_seconds = int(os.getenv("AUTH_REFRESH_TOKEN_TTL_SECONDS", "604800"))
    password_iterations = int(os.getenv("AUTH_PASSWORD_ITERATIONS", "210000"))
    jwt_secret = os.getenv("JWT_SECRET", "ticketing-dev-secret")
    jwt_issuer = os.getenv("JWT_ISSUER", service_name)
    jwt_roles = {"CUSTOMER", "PROVIDER", "ADMIN"}
    expose_demo_accounts = os.getenv("AUTH_EXPOSE_DEMO_ACCOUNTS", "true").lower() == "true"

    def observability_config(self) -> ObservabilityConfig:
        return observability_config_from_env(self.service_name, env=os.environ)


settings = Settings()
