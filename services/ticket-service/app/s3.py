import boto3
import qrcode
import io
from app.config import settings


def _s3_client():
    return boto3.client(
        "s3",
        aws_access_key_id=settings.aws_access_key_id,
        aws_secret_access_key=settings.aws_secret_access_key,
        region_name=settings.aws_region,
    )


def upload_qr(ticket_id: int, reservation_id: str) -> str | None:
    if not settings.aws_access_key_id:
        return None

    # QR 코드 생성
    qr = qrcode.make(f"ticket:{ticket_id}:reservation:{reservation_id}")
    buffer = io.BytesIO()
    qr.save(buffer, format="PNG")
    buffer.seek(0)

    key = f"tickets/{ticket_id}/qr.png"
    _s3_client().upload_fileobj(buffer, settings.s3_bucket, key)
    return f"https://{settings.s3_bucket}.s3.{settings.aws_region}.amazonaws.com/{key}"


def upload_pdf(ticket_id: int, reservation_id: str) -> str | None:
    if not settings.aws_access_key_id:
        return None

    # 간단한 텍스트 PDF 생성 (실제 프로젝트에서는 reportlab 등 사용)
    content = f"Ticket ID: {ticket_id}\nReservation ID: {reservation_id}".encode("utf-8")
    buffer = io.BytesIO(content)

    key = f"tickets/{ticket_id}/ticket.pdf"
    _s3_client().upload_fileobj(buffer, settings.s3_bucket, key)
    return f"https://{settings.s3_bucket}.s3.{settings.aws_region}.amazonaws.com/{key}"
