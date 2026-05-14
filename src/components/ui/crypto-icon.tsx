interface CryptoIconProps {
  currency: string;
  size?: number;
  className?: string;
}

const svgModules = import.meta.glob(
  "/node_modules/cryptocurrency-icons/svg/color/*.svg",
  { eager: true, query: "?url", import: "default" },
) as Record<string, string>;

const iconUrlMap: Record<string, string> = {};
for (const [path, url] of Object.entries(svgModules)) {
  const match = path.match(/\/([^/]+)\.svg$/);
  if (match) iconUrlMap[match[1].toLowerCase()] = url;
}

export function CryptoIcon({ currency, size = 16, className = "" }: CryptoIconProps) {
  const url = iconUrlMap[currency.toLowerCase()];

  if (!url) {
    return (
      <div
        className={`rounded-full flex items-center justify-center font-bold text-white ${className}`}
        style={{
          width: size,
          height: size,
          backgroundColor: "#888",
          fontSize: size * 0.4,
        }}
      >
        {currency.charAt(0)}
      </div>
    );
  }

  return (
    <img
      src={url}
      alt={currency}
      width={size}
      height={size}
      className={className}
    />
  );
}

export default CryptoIcon;
