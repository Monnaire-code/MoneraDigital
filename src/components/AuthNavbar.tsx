import { useState, useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import LanguageSwitcher from "./LanguageSwitcher";
import { LogOut, User } from "lucide-react";
import { useTranslation } from "react-i18next";

const AuthNavbar = () => {
  const [user, setUser] = useState<{ email: string } | null>(null);
  const { t } = useTranslation();
  const navigate = useNavigate();

  useEffect(() => {
    const savedUser = localStorage.getItem("user");
    if (savedUser && savedUser !== "undefined" && savedUser !== "null") {
      try {
        setUser(JSON.parse(savedUser));
      } catch (e) {
        console.error("Failed to parse user from localStorage", e);
      }
    }
  }, []);

  const handleLogout = () => {
    localStorage.removeItem("token");
    localStorage.removeItem("user");
    setUser(null);
    navigate("/");
  };

  return (
    <header className="fixed top-0 left-0 right-0 z-50 glass">
      <div className="container mx-auto px-6">
        <div className="flex items-center justify-between h-16 lg:h-20">
          <Link to="/" className="flex items-center gap-3">
            <img src="/m-logo-new.png" alt="Monera Digital" className="w-8 h-8 object-contain" />
            <span className="text-foreground font-semibold text-xl tracking-tight">
              Monera<span className="text-primary">Digital</span>
            </span>
          </Link>

          <div className="flex items-center gap-4">
            <LanguageSwitcher />
            {user && (
              <div className="flex items-center gap-3">
                <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                  <User size={16} className="text-primary" />
                  <span className="hidden sm:inline">{user.email}</span>
                </div>
                <Button variant="ghost" size="sm" onClick={handleLogout} className="gap-2">
                  <LogOut size={16} />
                  <span className="hidden sm:inline">{t("auth.login.logout")}</span>
                </Button>
              </div>
            )}
          </div>
        </div>
      </div>
    </header>
  );
};

export default AuthNavbar;
