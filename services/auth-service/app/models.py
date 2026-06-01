from datetime import datetime

from sqlalchemy import Boolean, DateTime, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column

from app.database import Base


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    email: Mapped[str] = mapped_column(String(180), unique=True, index=True, nullable=False)
    password_hash: Mapped[str] = mapped_column(String(300), nullable=False)
    display_name: Mapped[str] = mapped_column(String(100), nullable=False)
    role: Mapped[str] = mapped_column(String(30), index=True, nullable=False)
    is_active: Mapped[bool] = mapped_column(Boolean, nullable=False, default=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    @property
    def displayName(self) -> str:
        return self.display_name

    @property
    def isActive(self) -> bool:
        return self.is_active

    @property
    def createdAt(self) -> datetime:
        return self.created_at


class AuditLog(Base):
    __tablename__ = "audit_logs"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    event_type: Mapped[str] = mapped_column(String(80), index=True, nullable=False)
    outcome: Mapped[str] = mapped_column(String(30), index=True, nullable=False)
    user_id: Mapped[int | None] = mapped_column(Integer, index=True, nullable=True)
    user_email: Mapped[str | None] = mapped_column(String(180), nullable=True)
    role: Mapped[str | None] = mapped_column(String(30), nullable=True)
    request_id: Mapped[str | None] = mapped_column(String(120), nullable=True)
    method: Mapped[str | None] = mapped_column(String(20), nullable=True)
    path: Mapped[str | None] = mapped_column(String(300), nullable=True)
    client_ip: Mapped[str | None] = mapped_column(String(80), nullable=True)
    user_agent: Mapped[str | None] = mapped_column(String(500), nullable=True)
    details: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    @property
    def eventType(self) -> str:
        return self.event_type

    @property
    def userId(self) -> int | None:
        return self.user_id

    @property
    def userEmail(self) -> str | None:
        return self.user_email

    @property
    def requestId(self) -> str | None:
        return self.request_id

    @property
    def clientIp(self) -> str | None:
        return self.client_ip

    @property
    def userAgent(self) -> str | None:
        return self.user_agent

    @property
    def createdAt(self) -> datetime:
        return self.created_at


class RevokedToken(Base):
    __tablename__ = "revoked_tokens"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    token_id: Mapped[str] = mapped_column(String(120), unique=True, index=True, nullable=False)
    user_id: Mapped[int | None] = mapped_column(Integer, index=True, nullable=True)
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())


class RefreshToken(Base):
    __tablename__ = "refresh_tokens"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    token_hash: Mapped[str] = mapped_column(String(120), unique=True, index=True, nullable=False)
    user_id: Mapped[int] = mapped_column(Integer, index=True, nullable=False)
    expires_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), nullable=False)
    revoked_at: Mapped[datetime | None] = mapped_column(DateTime(timezone=True), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
