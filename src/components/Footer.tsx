import { Twitter, Linkedin, Mail } from "lucide-react";
import { useTranslation } from "react-i18next";

const Footer = () => {
  const { t } = useTranslation();

  const footerLinks = {
    products: [
      { label: t("footer.links.structuredLending"), href: "#" },
      { label: t("footer.links.fixedIncome"), href: "#" },
      { label: t("footer.links.yieldProducts"), href: "#" },
      { label: t("footer.links.custodyServices"), href: "#" },
    ],
    company: [
      { label: t("footer.links.aboutUs"), href: "#" },
      { label: t("footer.links.careers"), href: "#" },
      { label: t("footer.links.press"), href: "#" },
      { label: t("footer.links.contact"), href: "#" },
    ],
    resources: [
      { label: t("footer.links.documentation"), href: "#" },
      { label: t("footer.links.research"), href: "#" },
      { label: t("footer.links.api"), href: "#" },
      { label: t("footer.links.status"), href: "#" },
    ],
    legal: [
      { label: t("footer.links.privacyPolicy"), href: "#" },
      { label: t("footer.links.termsOfService"), href: "#" },
      { label: t("footer.links.riskDisclosure"), href: "#" },
      { label: t("footer.links.compliance"), href: "#" },
    ],
  };

  const socialLinks = [
    { icon: Twitter, href: "https://x.com/Monera_Digital", label: "Twitter" },
    { icon: Linkedin, href: "https://www.linkedin.com/company/monera-digital/posts/?feedView=all", label: "LinkedIn" },
    { icon: Mail, href: "mailto:contact@moneradigital.com", label: "Email" },
  ];

  return (
    <footer className="border-t border-border bg-card/30">
      <div className="container mx-auto px-6 py-16">
        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-8 mb-12">
          {/* Brand Column */}
          <div className="col-span-2">
            <a href="/" className="flex items-center gap-3 mb-4">
              <div className="w-8 h-8 rounded-lg bg-gradient-primary flex items-center justify-center">
                <span className="text-primary-foreground font-bold text-lg">M</span>
              </div>
              <span className="text-foreground font-semibold text-xl tracking-tight">
                Monera<span className="text-primary">Digital</span>
              </span>
            </a>
            <p className="text-muted-foreground text-sm leading-relaxed mb-6 max-w-xs">
              {t("footer.tagline")}
            </p>
            <div className="flex items-center gap-3">
              {socialLinks.map((social) => (
                <a
                  key={social.label}
                  href={social.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="w-10 h-10 rounded-lg bg-secondary hover:bg-primary hover:text-primary-foreground flex items-center justify-center text-foreground transition-all duration-200 shadow-sm hover:shadow-md hover:scale-105"
                  aria-label={social.label}
                >
                  <social.icon className="w-5 h-5" />
                </a>
              ))}
            </div>
          </div>

          {/* Products */}
          <div>
            <h4 className="text-foreground font-semibold mb-4">{t("footer.sections.products")}</h4>
            <ul className="space-y-3">
              {footerLinks.products.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-muted-foreground hover:text-primary transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>

          {/* Company */}
          <div>
            <h4 className="text-foreground font-semibold mb-4">{t("footer.sections.company")}</h4>
            <ul className="space-y-3">
              {footerLinks.company.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-muted-foreground hover:text-primary transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>

          {/* Resources */}
          <div>
            <h4 className="text-foreground font-semibold mb-4">{t("footer.sections.resources")}</h4>
            <ul className="space-y-3">
              {footerLinks.resources.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-muted-foreground hover:text-primary transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>

          {/* Legal */}
          <div>
            <h4 className="text-foreground font-semibold mb-4">{t("footer.sections.legal")}</h4>
            <ul className="space-y-3">
              {footerLinks.legal.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-muted-foreground hover:text-primary transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        </div>

        {/* Bottom Bar */}
        <div className="pt-8 border-t border-border flex flex-col md:flex-row items-center justify-between gap-4">
          <p className="text-sm text-muted-foreground">
            {t("footer.copyright", { year: new Date().getFullYear() })}
          </p>
          <p className="text-xs text-muted-foreground/60 max-w-lg text-center md:text-right">
            {t("footer.riskDisclaimer")}
          </p>
        </div>
      </div>
    </footer>
  );
};

export default Footer;
