from datetime import datetime
from sqlalchemy import DateTime, Integer, String, func
from sqlalchemy.orm import Mapped, mapped_column
from app.database import Base


class Ticket(Base):
    __tablename__ = "tickets"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    reservation_id: Mapped[str] = mapped_column(String(100), index=True, nullable=False, unique=True)
    user_id: Mapped[str] = mapped_column(String(120), index=True, nullable=False)
    concert_id: Mapped[str] = mapped_column(String(100), nullable=False)
    seat_id: Mapped[str] = mapped_column(String(100), nullable=False)
    status: Mapped[str] = mapped_column(String(30), nullable=False, default="ISSUED")
    qr_url: Mapped[str | None] = mapped_column(String(500), nullable=True)
    pdf_url: Mapped[str | None] = mapped_column(String(500), nullable=True)
    issued_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())

    @property
    def reservationId(self) -> str:
        return self.reservation_id

    @property
    def userId(self) -> str:
        return self.user_id

    @property
    def concertId(self) -> str:
        return self.concert_id

    @property
    def seatId(self) -> str:
        return self.seat_id

    @property
    def qrUrl(self) -> str | None:
        return self.qr_url

    @property
    def pdfUrl(self) -> str | None:
        return self.pdf_url

    @property
    def issuedAt(self) -> datetime:
        return self.issued_at


class ProcessedEvent(Base):
    __tablename__ = "processed_events"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, index=True)
    event_id: Mapped[str] = mapped_column(String(100), unique=True, nullable=False)
    ticket_id: Mapped[int] = mapped_column(Integer, nullable=False)
    created_at: Mapped[datetime] = mapped_column(DateTime(timezone=True), server_default=func.now())
