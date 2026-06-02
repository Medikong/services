import os

from server.observability import ObservabilityConfig, observability_config_from_env


class Settings:
    service_name = "payment-service"
    database_url = os.getenv("DATABASE_URL", "sqlite:///./payment_service.db")
    jwt_secret = os.getenv("JWT_SECRET", "ticketing-dev-secret")
    jwt_issuer = os.getenv("JWT_ISSUER", "auth-service")
    jwt_roles = {"CUSTOMER", "PROVIDER", "ADMIN"}
    kafka_bootstrap_servers = os.getenv("KAFKA_BOOTSTRAP_SERVERS", "")
    payment_approved_topic = os.getenv("PAYMENT_APPROVED_TOPIC", "payment-approved")
    payment_failed_topic = os.getenv("PAYMENT_FAILED_TOPIC", "payment-failed")

    def observability_config(self) -> ObservabilityConfig:
        return observability_config_from_env(self.service_name, env=os.environ)


settings = Settings()
