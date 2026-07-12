"use client";

import { useEffect, useState } from "react";

type CountdownProps = {
  endsAt: string | null;
  serverNow: string;
};

export function Countdown({ endsAt, serverNow }: CountdownProps) {
  const [elapsedSinceServerRender, setElapsedSinceServerRender] = useState(0);

  useEffect(() => {
    const browserStartedAt = Date.now();
    const timer = window.setInterval(() => setElapsedSinceServerRender(Date.now() - browserStartedAt), 1000);
    return () => window.clearInterval(timer);
  }, []);

  if (!endsAt) {
    return <span className="countdown">오픈 시간을 확인하는 중</span>;
  }

  const serverTimestamp = new Date(serverNow).getTime();
  const remainingSeconds = Math.max(0, Math.floor((new Date(endsAt).getTime() - (serverTimestamp + elapsedSinceServerRender)) / 1000));
  const hours = Math.floor(remainingSeconds / 3600).toString().padStart(2, "0");
  const minutes = Math.floor((remainingSeconds % 3600) / 60).toString().padStart(2, "0");
  const seconds = (remainingSeconds % 60).toString().padStart(2, "0");

  return <span className="countdown">{hours} : {minutes} : {seconds}</span>;
}
