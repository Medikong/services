import type { Metadata } from "next";

import "./globals.css";

export const metadata: Metadata = {
  title: "DropMong | 한정 드롭",
  description: "한정 수량 드롭을 발견하고 구매하는 DropMong 구매자 웹",
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="ko">
      <body>
        <a className="skip-link" href="#main-content">본문으로 건너뛰기</a>
        {children}
      </body>
    </html>
  );
}
