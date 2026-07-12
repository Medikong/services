type ProductArtProps = {
  className?: string;
  tone?: "dark" | "gray" | "purple";
};

export function ProductArt({ className = "", tone = "dark" }: ProductArtProps) {
  return (
    <div aria-hidden="true" className={`product-art product-art--${tone} ${className}`}>
      <span className="product-art__halo" />
      <span className="product-art__sleeve product-art__sleeve--left" />
      <span className="product-art__body">
        <span className="product-art__zip" />
        <span className="product-art__mark product-art__mark--left">DMG</span>
        <span className="product-art__mark product-art__mark--right">DMG</span>
      </span>
      <span className="product-art__sleeve product-art__sleeve--right" />
    </div>
  );
}
