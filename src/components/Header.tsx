import { Button } from "@/components/ui/button";
import { Menu, X, LogOut, User } from "lucide-react";
import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "react-router-dom";
import LanguageSwitcher from "./LanguageSwitcher";

const Header = () => {
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const [user, setUser] = useState<{ email: string } | null>(null);
  const { t } = useTranslation();
  const navigate = useNavigate();

  useEffect(() => {
    const savedUser = localStorage.getItem("user");
    // Check for null, undefined string, or empty string before parsing
    if (savedUser && savedUser !== "undefined" && savedUser !== "null") {
      try {
        setUser(JSON.parse(savedUser));
      } catch (e) {
        console.error("Failed to parse user from localStorage", e);
        // Clean up invalid data
        localStorage.removeItem("user");
      }
    }
  }, []);

  const handleLogout = () => {
    localStorage.removeItem("token");
    localStorage.removeItem("user");
    setUser(null);
    navigate("/");
  };

  const navItems = [
    { label: t("header.nav.products"), href: "#products" },
    { label: t("header.nav.structuredProducts"), href: "/structured-products" },
    { label: t("header.nav.solutions"), href: "#solutions" },
    { label: t("header.nav.about"), href: "#about" },
    { label: t("header.nav.insights"), href: "#insights" },
  ];

  return (
    <header className="fixed top-0 left-0 right-0 z-50 glass">
      <div className="container mx-auto px-6">
        <div className="flex items-center justify-between h-16 lg:h-20">
          {/* Logo */}
          <Link to="/" className="flex items-center gap-3">
            <img src="/m-logo-new.png" alt="Monera Digital" className="w-8 h-8 object-contain" />
            <span className="text-foreground font-semibold text-xl tracking-tight">
              Monera<span className="text-primary">Digital</span>
            </span>
          </Link>

          {/* Desktop Navigation */}
          <nav className="hidden lg:flex items-center gap-8">
            {navItems.map((item) => (
              <a
                key={item.label}
                href={item.href}
                className="text-muted-foreground hover:text-foreground transition-colors duration-200 text-sm font-medium"
              >
                {item.label}
              </a>
            ))}
          </nav>

          {/* Desktop CTA */}
          <div className="hidden lg:flex items-center gap-4">
            <LanguageSwitcher />
            {user ? (
              <div className="flex items-center gap-4">
                <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                  <User size={16} className="text-primary" />
                  <span>{user.email}</span>
                </div>
                <Button variant="ghost" size="sm" onClick={handleLogout} className="gap-2">
                  <LogOut size={16} />
                  {t("auth.login.logout")}
                </Button>
              </div>
            ) : (
              <>
                <Button variant="ghost" size="sm" asChild>
                  <Link to="/login">{t("header.auth.signIn")}</Link>
                </Button>
                <Button variant="default" size="sm" asChild>
                  <Link to="/register">{t("header.auth.getStarted")}</Link>
                </Button>
              </>
            )}
          </div>

          {/* Mobile Menu Button */}
          <button
            className="lg:hidden text-foreground p-2"
            onClick={() => setIsMenuOpen(!isMenuOpen)}
          >
            {isMenuOpen ? <X size={24} /> : <Menu size={24} />}
          </button>
        </div>

        {/* Mobile Navigation */}
        {isMenuOpen && (
          <div className="lg:hidden py-4 border-t border-border animate-fade-in">
            <nav className="flex flex-col gap-4">
              {navItems.map((item) => (
                item.href.startsWith("/") ? (
                  <Link
                    key={item.label}
                    to={item.href}
                    className="text-muted-foreground hover:text-foreground transition-colors duration-200 text-sm font-medium py-2"
                    onClick={() => setIsMenuOpen(false)}
                  >
                    {item.label}
                  </Link>
                ) : (
                  <a
                    key={item.label}
                    href={item.href}
                    className="text-muted-foreground hover:text-foreground transition-colors duration-200 text-sm font-medium py-2"
                    onClick={() => setIsMenuOpen(false)}
                  >
                    {item.label}
                  </a>
                )
              ))}
              <div className="flex flex-col gap-3 pt-4 border-t border-border">
                <div className="px-2">
                  <LanguageSwitcher />
                </div>
                {user ? (
                  <>
                    <div className="flex items-center gap-2 text-sm font-medium text-foreground px-2 py-2">
                      <User size={16} className="text-primary" />
                      <span>{user.email}</span>
                    </div>
                    <Button variant="ghost" size="sm" className="justify-start gap-2" onClick={() => { handleLogout(); setIsMenuOpen(false); }}>
                      <LogOut size={16} />
                      {t("auth.login.logout")}
                    </Button>
                  </>
                ) : (
                  <>
                    <Button variant="ghost" size="sm" className="justify-start" asChild onClick={() => setIsMenuOpen(false)}>
                      <Link to="/login">{t("header.auth.signIn")}</Link>
                    </Button>
                    <Button variant="default" size="sm" asChild onClick={() => setIsMenuOpen(false)}>
                      <Link to="/register">{t("header.auth.getStarted")}</Link>
                    </Button>
                  </>
                )}
              </div>
            </nav>
          </div>
        )}
      </div>
    </header>
  );
};

export default Header;
