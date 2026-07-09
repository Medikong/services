from enum import StrEnum


class CatalogResource(StrEnum):
    CONCERTS = "concerts"
    CONCERT = "concert"
    PERFORMANCES = "performances"
    SEATS = "seats"
    VENUES = "venues"
    SALE_POLICY = "sale_policy"
    REVIEW_STATUS = "review_status"
    REVIEW_REQUESTS = "review_requests"


class ConcertAdminCommand(StrEnum):
    CREATE_CONCERT = "create_concert"
    UPDATE_CONCERT = "update_concert"
    CREATE_VENUE = "create_venue"
    CREATE_SHOWTIME = "create_showtime"
    UPDATE_SHOWTIME = "update_showtime"
    UPDATE_SALE_POLICY = "update_sale_policy"
    SUBMIT_OPEN_REQUEST = "submit_open_request"
    UPDATE_OPEN_SCHEDULE = "update_open_schedule"
    APPROVE_SALE_POLICY = "approve_sale_policy"
    REJECT_SALE_POLICY = "reject_sale_policy"
    SET_REOPEN_POLICY = "set_reopen_policy"
    APPROVE_REVIEW_REQUEST = "approve_review_request"
    REJECT_REVIEW_REQUEST = "reject_review_request"


class SeatInventoryCommand(StrEnum):
    UPLOAD_SEAT_MAP = "upload_seat_map"
    UPDATE_SEAT_INVENTORY = "update_seat_inventory"
    CREATE_SEAT_GRADES = "create_seat_grades"
    CREATE_HOLD_REQUEST = "create_hold_request"
