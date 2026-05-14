import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import i18n from "@/i18n";
import Footer from "./Footer";

const renderFooter = () =>
  render(
    <I18nextProvider i18n={i18n}>
      <Footer />
    </I18nextProvider>
  );

describe("Footer", () => {
  it("renders social links with correct hrefs", () => {
    renderFooter();
    const links = screen.getAllByRole("link");
    const hrefs = links.map((l) => l.getAttribute("href"));

    expect(hrefs).toContain("https://x.com/Monera_Digital");
    expect(hrefs).toContain("https://www.linkedin.com/company/monera-digital/posts/?feedView=all");
    expect(hrefs).toContain("mailto:contact@moneradigital.com");
  });

  it("renders social links with correct target and rel", () => {
    renderFooter();
    const socialLinks = screen.getAllByRole("link").filter(
      (l) => l.getAttribute("href")?.startsWith("http")
    );
    for (const link of socialLinks) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    }
  });

  it("renders footer brand", () => {
    renderFooter();
    // Check for footer element existence
    expect(document.querySelector("footer")).toBeInTheDocument();
  });

  it("renders copyright", () => {
    renderFooter();
    expect(screen.getByText(/© \d{4} Monera Digital/)).toBeInTheDocument();
  });
});