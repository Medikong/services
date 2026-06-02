import os

from pydantic_settings import BaseSettings, SettingsConfigDict
from server.observability import ObservabilityConfig, observability_config_from_env


class Settings(BaseSettings):
    service_name: str = "reservation-service"
    port: int = 8083
    database_url: str = "sqlite:///./reservation_service.db"
    kafka_bootstrap_servers: str = ""
    reservation_created_topic: str = "reservation-created"
    reservation_expired_topic: str = "reservation-expired"

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    def observability_config(self) -> ObservabilityConfig:
        return observability_config_from_env(self.service_name, env=os.environ)


settings = Settings()
