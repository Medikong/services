"use client";

import { SellerRouteProblem } from "@/components/seller/seller-route-problem";

export default function SellerParentError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) { return <main id="main-content"><SellerRouteProblem error={error} reset={reset} /></main>; }
