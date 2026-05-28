from app import schemas
from app.exceptions import ConflictError
from app.services.base import ReservationDomainService, now_utc
from app.services.serializers import sales_command_response


class SalesService(ReservationDomainService):
    def sales_summary(self, concert_id: str) -> schemas.SalesSummaryResponse:
        state = self.sales.get_or_create_sales_state(concert_id, now_utc())
        sold, reserved = self.sales.reservation_counts_for_concert(concert_id)
        self.commit()
        return schemas.SalesSummaryResponse(
            concertId=concert_id,
            salesStatus=state.sales_status,
            totalSeats=max(state.total_seats, sold + reserved),
            soldSeats=sold,
            reservedSeats=reserved,
            grossAmount=0,
            updatedAt=state.updated_at,
        )

    def showtime_sales_summary(self, showtime_id: str) -> schemas.ShowtimeSalesResponse:
        sold, reserved = self.sales.reservation_counts_for_showtime(showtime_id)
        total = sold + reserved
        return schemas.ShowtimeSalesResponse(
            showtimeId=showtime_id,
            totalSeats=total,
            availableSeats=0,
            soldSeats=sold,
            reservedSeats=reserved,
            grossAmount=0,
            updatedAt=now_utc(),
        )

    def start_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        state = self.sales.get_or_create_sales_state(concert_id, now_utc())
        if state.sales_status == "open":
            raise ConflictError("sales.invalid_state", "Sales are already open.")
        if state.sales_status == "closed":
            raise ConflictError("sales.invalid_state", "Closed sales cannot be started.")
        state.sales_status = "open"
        state.updated_at = now_utc()
        self.commit()
        return sales_command_response(state)

    def pause_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        state = self._sales_state(concert_id)
        if state.sales_status != "open":
            raise ConflictError("sales.invalid_state", "Only open sales can be paused.")
        state.sales_status = "paused"
        state.updated_at = now_utc()
        self.commit()
        return sales_command_response(state)

    def resume_sales(self, concert_id: str) -> schemas.SalesCommandResponse:
        state = self._sales_state(concert_id)
        if state.sales_status != "paused":
            raise ConflictError("sales.invalid_state", "Only paused sales can be resumed.")
        state.sales_status = "open"
        state.updated_at = now_utc()
        self.commit()
        return sales_command_response(state)
