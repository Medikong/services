import os

from pydantic_settings import BaseSettings, SettingsConfigDict
from server.observability import ObservabilityConfig, observability_config_from_env


class Settings(BaseSettings):
    service_name: str = "concert-service"
    port: int = 8082
    database_url: str = "sqlite:///./concert_service.db"

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8", extra="ignore")

    def observability_config(self) -> ObservabilityConfig:
        return observability_config_from_env(self.service_name, env=os.environ)


settings = Settings()
