import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import i18n from "@/i18n";
import Links from "./Links";

const renderLinks = () =>
  render(
    <I18nextProvider i18n={i18n}>
      <Links />
    </I18nextProvider>
  );

describe("Links", () => {
  it("renders the profile section", () => {
    renderLinks();
    expect(screen.getByText("Monera Digital")).toBeInTheDocument();
    expect(screen.getByText("Institutional Digital Asset Platform")).toBeInTheDocument();
  });

  it("renders Social Media section with all 5 links", () => {
    renderLinks();
    expect(screen.getByText("Social Media")).toBeInTheDocument();
    expect(screen.getByText("Website")).toBeInTheDocument();
    expect(screen.getByText("Twitter")).toBeInTheDocument();
    expect(screen.getByText("Twitter Analyst")).toBeInTheDocument();
    expect(screen.getByText("Telegram")).toBeInTheDocument();
    expect(screen.getByText("LinkedIn")).toBeInTheDocument();
  });

  it("renders Telegram Channels section with all 4 links", () => {
    renderLinks();
    expect(screen.getByText("Telegram Channels")).toBeInTheDocument();
    expect(screen.getByText("Official Announcements")).toBeInTheDocument();
    expect(screen.getByText("Market News")).toBeInTheDocument();
    expect(screen.getByText("China")).toBeInTheDocument();
    expect(screen.getByText("US")).toBeInTheDocument();
  });

  it("renders correct hrefs for all links", () => {
    renderLinks();
    const links = screen.getAllByRole("link");
    const hrefs = links.map((l) => l.getAttribute("href"));

    // Social Media
    expect(hrefs).toContain("https://www.moneradigital.com/");
    expect(hrefs).toContain("https://x.com/Monera_Digital");
    expect(hrefs).toContain("https://x.com/MoneraAnalyst");
    expect(hrefs).toContain("https://t.me/MoneraDigital_Official");
    expect(hrefs).toContain("https://www.linkedin.com/company/monera-digital/posts/?feedView=all");

    // Telegram Channels
    expect(hrefs).toContain("https://t.me/MoneraDigitalhe/16144");
    expect(hrefs).toContain("https://t.me/MoneraDigitalhe/16149");
    expect(hrefs).toContain("https://t.me/MoneraDigitalhe/11034");
    expect(hrefs).toContain("https://t.me/MoneraDigitalhe/1");
  });

  it("renders 10 links total (9 social links + 1 header logo)", () => {
    renderLinks();
    const links = screen.getAllByRole("link");
    expect(links).toHaveLength(10);
  });

  it("renders risk disclaimer footer", () => {
    renderLinks();
    expect(screen.getByText(/risk/i)).toBeInTheDocument();
  });

  it("renders all social media links with correct target and rel", () => {
    renderLinks();
    // Exclude header logo link (href="/"), only check 9 social links
    const links = screen.getAllByRole("link").filter((l) => l.getAttribute("href") !== "/");
    expect(links).toHaveLength(9);
    for (const link of links) {
      expect(link).toHaveAttribute("target", "_blank");
      expect(link).toHaveAttribute("rel", "noopener noreferrer");
    }
  });
});