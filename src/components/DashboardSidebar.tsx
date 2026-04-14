import { useTranslation } from "react-i18next";
import { Link, useLocation } from "react-router-dom";
import {
  LayoutDashboard,
  Wallet,
  ArrowUpCircle,
  TrendingUp,
  ShieldCheck,
  ChevronLeft,
  ChevronRight,
  Bitcoin
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useState } from "react";

const DashboardSidebar = () => {
  const { t } = useTranslation();
  const location = useLocation();
  const [isCollapsed, setIsCollapsed] = useState(false);

  const menuItems = [
    { icon: LayoutDashboard, label: t("dashboard.nav.overview"), href: "/dashboard" },
    { icon: Wallet, label: t("dashboard.nav.assets"), href: "/dashboard/assets" },
    { icon: ArrowUpCircle, label: t("dashboard.nav.withdraw"), href: "/dashboard/withdraw" },
    { icon: Bitcoin, label: t("dashboard.nav.fixedDeposit"), href: "/dashboard/fixed-deposit" },
    { icon: ShieldCheck, label: t("dashboard.nav.security"), href: "/dashboard/security" },
  ];

  return (
    <aside 
      className={cn(
        "relative h-screen bg-card border-r border-border transition-all duration-300 flex flex-col z-40",
        isCollapsed ? "w-20" : "w-64"
      )}
    >
      {/* Logo Area */}
      <div className="p-6 flex items-center gap-3">
        <img src="/m-logo-new.png" alt="Monera Digital" className="w-8 h-8 object-contain shrink-0" />
        {!isCollapsed && (
          <span className="text-foreground font-semibold text-xl tracking-tight truncate">
            Monera<span className="text-primary">Digital</span>
          </span>
        )}
      </div>

      {/* Navigation */}
      <nav className="flex-1 px-3 space-y-2 mt-4">
        {menuItems.map((item) => {
          const isActive = location.pathname === item.href;
          return (
            <Link
              key={item.href}
              to={item.href}
              className={cn(
                "flex items-center gap-3 px-3 py-2 rounded-lg transition-colors group relative",
                isActive 
                  ? "bg-primary text-primary-foreground" 
                  : "text-muted-foreground hover:text-foreground hover:bg-secondary"
              )}
            >
              <item.icon size={20} className="shrink-0" />
              {!isCollapsed && <span className="font-medium text-sm">{item.label}</span>}
              {isCollapsed && (
                <div className="absolute left-14 bg-popover text-popover-foreground px-2 py-1 rounded text-xs opacity-0 group-hover:opacity-100 pointer-events-none transition-opacity whitespace-nowrap z-50 shadow-md">
                  {item.label}
                </div>
              )}
            </Link>
          );
        })}
      </nav>

      {/* Collapse Toggle */}
      <button 
        onClick={() => setIsCollapsed(!isCollapsed)}
        className="p-4 border-t border-border flex items-center justify-center hover:bg-secondary transition-colors"
      >
        {isCollapsed ? <ChevronRight size={20} /> : <ChevronLeft size={20} />}
      </button>
    </aside>
  );
};

export default DashboardSidebar;
