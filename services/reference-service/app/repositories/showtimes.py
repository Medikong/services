"""Showtime persistence queries."""
from collections.abc import Sequence
from datetime import datetime
from typing import NamedTuple

from sqlalchemy import case, func, select
from sqlalchemy.orm import Session, selectinload

from app import entities as model


class SeatMapShowtimeRow(NamedTuple):
    id: str
    starts_at: datetime
    venue_id: str
    venue_name: str


class SeatMapGradeRow(NamedTuple):
    name: str
    price: int


class SeatMapSectionRow(NamedTuple):
    section: str
    total_count: int
    available_count: int


class SeatMapSeatRow(NamedTuple):
    id: str
    section: str
    row_label: str
    number: str
    status: str


class ShowtimeRepository:
    def __init__(self, db: Session) -> None:
        self.db = db

    def get_showtime(self, showtime_id: str) -> model.Showtime | None:
        return self.db.scalar(
            select(model.Showtime)
            .options(
                selectinload(model.Showtime.seats),
                selectinload(model.Showtime.seat_grades),
                selectinload(model.Showtime.venue),
            )
            .where(model.Showtime.id == showtime_id)
        )

    def get_seat_map_showtime(self, showtime_id: str) -> SeatMapShowtimeRow | None:
        row = self.db.execute(
            select(
                model.Showtime.id,
                model.Showtime.starts_at,
                model.Venue.id,
                model.Venue.name,
            )
            .join(model.Venue, model.Venue.id == model.Showtime.venue_id)
            .where(model.Showtime.id == showtime_id)
        ).one_or_none()
        if row is None:
            return None
        return SeatMapShowtimeRow(row[0], row[1], row[2], row[3])

    def list_seat_map_grades(self, showtime_id: str) -> Sequence[SeatMapGradeRow]:
        rows = self.db.execute(
            select(model.SeatGrade.name, model.SeatGrade.price)
            .where(model.SeatGrade.showtime_id == showtime_id)
            .order_by(model.SeatGrade.name)
        ).all()
        return [SeatMapGradeRow(row[0], row[1]) for row in rows]

    def list_seat_map_sections(self, showtime_id: str) -> Sequence[SeatMapSectionRow]:
        available_count = func.sum(case((model.Seat.status == "sellable", 1), else_=0))
        rows = self.db.execute(
            select(
                model.Seat.section,
                func.count(model.Seat.id),
                available_count,
            )
            .where(model.Seat.showtime_id == showtime_id)
            .group_by(model.Seat.section)
            .order_by(model.Seat.section)
        ).all()
        return [SeatMapSectionRow(row[0], int(row[1]), int(row[2] or 0)) for row in rows]

    def list_seat_map_seats(
        self,
        showtime_id: str,
        limit: int,
        offset: int,
        section_id: str | None,
    ) -> Sequence[SeatMapSeatRow]:
        statement = select(
            model.Seat.id,
            model.Seat.section,
            model.Seat.row_label,
            model.Seat.number,
            model.Seat.status,
        ).where(model.Seat.showtime_id == showtime_id)
        if section_id is not None:
            statement = statement.where(model.Seat.section == section_id)
        rows = self.db.execute(
            statement.order_by(model.Seat.section, model.Seat.row_label, model.Seat.number)
            .offset(offset)
            .limit(limit)
        ).all()
        return [SeatMapSeatRow(row[0], row[1], row[2], row[3], row[4]) for row in rows]

    def list_showtimes(self, concert_id: str, limit: int) -> Sequence[model.Showtime]:
        return self.db.scalars(
            select(model.Showtime)
            .where(model.Showtime.concert_id == concert_id)
            .order_by(model.Showtime.starts_at)
            .limit(limit)
        ).all()

    def list_showtimes_between(self, concert_id: str, start_at: datetime, end_at: datetime) -> Sequence[model.Showtime]:
        return self.db.scalars(
            select(model.Showtime)
            .where(
                model.Showtime.concert_id == concert_id,
                model.Showtime.starts_at >= start_at,
                model.Showtime.starts_at < end_at,
            )
            .order_by(model.Showtime.starts_at)
        ).all()

    def list_bookable_showtime_starts_between(self, concert_id: str, start_at: datetime, end_at: datetime) -> Sequence[datetime]:
        sellable_seat_exists = (
            select(model.Seat.id)
            .where(
                model.Seat.showtime_id == model.Showtime.id,
                model.Seat.status == "sellable",
            )
            .exists()
        )
        return self.db.scalars(
            select(model.Showtime.starts_at)
            .where(
                model.Showtime.concert_id == concert_id,
                model.Showtime.starts_at >= start_at,
                model.Showtime.starts_at < end_at,
                model.Showtime.status.not_in(("closed", "canceled", "sold_out")),
                sellable_seat_exists,
            )
            .order_by(model.Showtime.starts_at)
        ).all()
