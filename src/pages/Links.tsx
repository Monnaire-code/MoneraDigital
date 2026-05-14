import { X as Twitter, Send } from "lucide-react";
import { useTranslation } from "react-i18next";

interface SocialLink {
  icon: typeof Twitter;
  label: string;
  href: string;
  gradient: string;
}

const Links = () => {
  const { t } = useTranslation();

  const socialLinks: SocialLink[] = [
    {
      icon: Twitter,
      label: "Twitter",
      href: "https://x.com/Monera_Digital",
      gradient: "bg-gradient-to-r from-blue-500 to-sky-400 hover:from-blue-600 hover:to-sky-500",
    },
    {
      icon: Send,
      label: "Telegram",
      href: "https://t.me/MoneraDigital_Official",
      gradient: "bg-gradient-to-r from-blue-600 to-telegram-blue hover:from-blue-700 hover:to-blue-500",
    },
    {
      icon: Send,
      label: "Official Announcements",
      href: "https://t.me/MoneraDigital_Ann",
      gradient: "bg-gradient-to-r from-blue-500 to-cyan-500 hover:from-blue-600 hover:to-cyan-400",
    },
    {
      icon: Send,
      label: "Market News",
      href: "https://t.me/MoneraDigitalMarket",
      gradient: "bg-gradient-to-r from-emerald-500 to-teal-500 hover:from-emerald-600 hover:to-teal-400",
    },
    {
      icon: Send,
      label: "China Community",
      href: "https://t.me/MoneraDigitalChina",
      gradient: "bg-gradient-to-r from-red-500 to-orange-500 hover:from-red-600 hover:to-orange-400",
    },
    {
      icon: Send,
      label: "US Community",
      href: "https://t.me/MoneraDigitalUS",
      gradient: "bg-gradient-to-r from-violet-500 to-purple-500 hover:from-violet-600 hover:to-purple-400",
    },
  ];

  return (
    <div className="min-h-screen bg-background flex flex-col">
      {/* Header */}
      <header className="border-b border-border bg-card/50 backdrop-blur-sm">
        <div className="container mx-auto px-6 py-4">
          <a href="/" className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-gradient-primary flex items-center justify-center">
              <span className="text-primary-foreground font-bold text-lg">M</span>
            </div>
            <span className="text-foreground font-semibold text-xl tracking-tight">
              Monera<span className="text-primary">Digital</span>
            </span>
          </a>
        </div>
      </header>

      {/* Main Content */}
      <main className="flex-1 flex flex-col items-center justify-center py-16 px-6">
        <div className="w-full max-w-md space-y-8">
          {/* Profile Section */}
          <div className="text-center space-y-4">
            <div className="w-24 h-24 mx-auto rounded-full bg-gradient-primary flex items-center justify-center">
              <span className="text-primary-foreground font-bold text-3xl">M</span>
            </div>
            <div className="space-y-1">
              <h1 className="text-2xl font-bold text-foreground">
                Monera Digital
              </h1>
              <p className="text-muted-foreground">
                Institutional Digital Asset Platform
              </p>
            </div>
          </div>

          {/* Links */}
          <div className="space-y-4">
            {socialLinks.map((link) => (
          <a
            href="https://t.me/MoneraDigitalhe/16149"
            target="_blank"
            rel="noopener noreferrer"
            className="text-primary hover:underline"
          >
            Market News
          </a>
            ))}
          </div>

          {/* Footer Note */}
          <p className="text-center text-sm text-muted-foreground">
            {t("footer.riskDisclaimer")}
          </p>
        </div>
      </main>
    </div>
  );
};

export default Links;