import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { CryptoIcon } from "./crypto-icon";

describe("CryptoIcon", () => {
  describe("known coins render <img> with correct SVG", () => {
    const knownCoins = ["BTC", "ETH", "USDT", "USDC", "SOL", "BNB"];

    knownCoins.forEach((coin) => {
      it(`renders ${coin} as an <img>`, () => {
        render(<CryptoIcon currency={coin} size={24} />);
        const img = screen.getByRole("img", { name: coin });
        expect(img).toBeInTheDocument();
        expect(img.tagName).toBe("IMG");
        expect(img).toHaveAttribute("width", "24");
        expect(img).toHaveAttribute("height", "24");
        expect(img).toHaveAttribute(
          "src",
          expect.stringContaining("data:image/svg+xml"),
        );
      });
    });
  });

  describe("case insensitivity", () => {
    it("renders lowercase 'btc' the same as uppercase 'BTC'", () => {
      const { container: lower } = render(<CryptoIcon currency="btc" />);
      const { container: upper } = render(<CryptoIcon currency="BTC" />);
      const lowerImg = lower.querySelector("img");
      const upperImg = upper.querySelector("img");
      expect(lowerImg).toBeTruthy();
      expect(upperImg).toBeTruthy();
      expect(lowerImg!.getAttribute("src")).toBe(upperImg!.getAttribute("src"));
    });

    it("renders mixed case 'Eth' correctly", () => {
      render(<CryptoIcon currency="Eth" />);
      expect(screen.getByRole("img", { name: "Eth" })).toBeInTheDocument();
    });
  });

  describe("unknown coin fallback", () => {
    it("renders a div with first letter for unknown coin", () => {
      const { container } = render(<CryptoIcon currency="ZZZCOIN" size={32} />);
      const fallback = container.firstChild as HTMLElement;
      expect(fallback.tagName).toBe("DIV");
      expect(fallback.textContent).toBe("Z");
      expect(fallback.style.width).toBe("32px");
      expect(fallback.style.height).toBe("32px");
      expect(fallback.style.backgroundColor).toBe("rgb(136, 136, 136)");
    });

    it("does not render an <img> for unknown coin", () => {
      render(<CryptoIcon currency="NONEXIST" />);
      expect(screen.queryByRole("img")).toBeNull();
    });
  });

  describe("size prop", () => {
    it("defaults to 16px", () => {
      render(<CryptoIcon currency="ETH" />);
      const img = screen.getByRole("img", { name: "ETH" });
      expect(img).toHaveAttribute("width", "16");
      expect(img).toHaveAttribute("height", "16");
    });

    it("respects custom size", () => {
      render(<CryptoIcon currency="ETH" size={48} />);
      const img = screen.getByRole("img", { name: "ETH" });
      expect(img).toHaveAttribute("width", "48");
      expect(img).toHaveAttribute("height", "48");
    });

    it("applies size to fallback div", () => {
      const { container } = render(<CryptoIcon currency="UNKNOWN" size={40} />);
      const fallback = container.firstChild as HTMLElement;
      expect(fallback.style.width).toBe("40px");
      expect(fallback.style.height).toBe("40px");
      expect(fallback.style.fontSize).toBe("16px");
    });
  });

  describe("className prop", () => {
    it("passes className to <img> for known coin", () => {
      render(<CryptoIcon currency="BTC" className="my-icon" />);
      const img = screen.getByRole("img", { name: "BTC" });
      expect(img).toHaveClass("my-icon");
    });

    it("passes className to fallback div for unknown coin", () => {
      const { container } = render(
        <CryptoIcon currency="UNKNOWN" className="custom-class" />,
      );
      const fallback = container.firstChild as HTMLElement;
      expect(fallback.className).toContain("custom-class");
    });
  });
});
