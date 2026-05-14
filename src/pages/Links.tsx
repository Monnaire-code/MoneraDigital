import { X as Twitter, Send, Globe, Linkedin as LinkedinIcon } from "lucide-react";
import { useTranslation } from "react-i18next";

interface LinkItem {
  icon: typeof Twitter;
  labelKey: string;
  href: string;
  gradient: string;
}

interface LinkSection {
  titleKey: string;
  items: LinkItem[];
}

const Links = () => {
  const { t } = useTranslation();

  const sections: LinkSection[] = [
    {
      titleKey: "links.socialMedia",
      items: [
        {
          icon: Globe,
          labelKey: "links.website",
          href: "https://www.moneradigital.com/",
          gradient: "bg-gradient-to-r from-slate-600 to-slate-500 hover:from-slate-700 hover:to-slate-600",
        },
        {
          icon: Twitter,
          labelKey: "links.twitter",
          href: "https://x.com/Monera_Digital",
          gradient: "bg-gradient-to-r from-sky-500 to-blue-600 hover:from-sky-600 hover:to-blue-700",
        },
        {
          icon: Twitter,
          labelKey: "links.twitterAnalyst",
          href: "https://x.com/MoneraAnalyst",
          gradient: "bg-gradient-to-r from-cyan-600 to-blue-500 hover:from-cyan-700 hover:to-blue-600",
        },
        {
          icon: Send,
          labelKey: "links.telegram",
          href: "https://t.me/MoneraDigital_Official",
          gradient: "bg-gradient-to-r from-blue-600 to-telegram-blue hover:from-blue-700 hover:to-blue-500",
        },
        {
          icon: LinkedinIcon,
          labelKey: "links.linkedin",
          href: "https://www.linkedin.com/company/monera-digital/posts/?feedView=all",
          gradient: "bg-gradient-to-r from-blue-700 to-blue-500 hover:from-blue-800 hover:to-blue-600",
        },
      ],
    },
    {
      titleKey: "links.telegramChannels",
      items: [
        {
          icon: Send,
          labelKey: "links.officialAnnouncements",
          href: "https://t.me/MoneraDigitalhe/16144",
          gradient: "bg-gradient-to-r from-blue-500 to-cyan-500 hover:from-blue-600 hover:to-cyan-400",
        },
        {
          icon: Send,
          labelKey: "links.marketNews",
          href: "https://t.me/MoneraDigitalhe/16149",
          gradient: "bg-gradient-to-r from-emerald-500 to-teal-500 hover:from-emerald-600 hover:to-teal-400",
        },
        {
          icon: Send,
          labelKey: "links.china",
          href: "https://t.me/MoneraDigitalhe/11034",
          gradient: "bg-gradient-to-r from-red-500 to-orange-500 hover:from-red-600 hover:to-orange-400",
        },
        {
          icon: Send,
          labelKey: "links.us",
          href: "https://t.me/MoneraDigitalhe/1",
          gradient: "bg-gradient-to-r from-violet-500 to-purple-500 hover:from-violet-600 hover:to-purple-400",
        },
      ],
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

          {/* Link Sections */}
          {sections.map((section) => (
            <div key={section.titleKey} className="space-y-4">
              <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider px-1">
                {t(section.titleKey)}
              </h2>
              <div className="space-y-3">
                {section.items.map((item) => (
                  <a
                    key={item.labelKey}
                    href={item.href}
                    target="_blank"
                    rel="noopener noreferrer"
                    className={`flex items-center gap-4 p-4 rounded-xl text-primary-foreground font-semibold transition-all duration-200 ${item.gradient} shadow-lg hover:shadow-xl hover:scale-[1.02] active:scale-[0.98]`}
                  >
                    <item.icon className="w-5 h-5 flex-shrink-0" />
                    <span className="flex-1">{t(item.labelKey)}</span>
                  </a>
                ))}
              </div>
            </div>
          ))}

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