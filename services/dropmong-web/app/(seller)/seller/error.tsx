"use client";

import { SellerRouteProblem } from "@/components/seller/seller-route-problem";

export default function SellerPageError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) { return <SellerRouteProblem error={error} reset={reset} />; }
