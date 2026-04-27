import { Outlet, useNavigate } from "react-router-dom";
import { useEffect, useState } from "react";
import DashboardSidebar from "./DashboardSidebar";
import LanguageSwitcher from "./LanguageSwitcher";
import { User, Bell, LogOut } from "lucide-react";
import { Button } from "./ui/button";

const DashboardLayout = () => {
  const navigate = useNavigate();
  const [user, setUser] = useState<{ email: string } | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    const checkAuth = async () => {
      const savedUser = localStorage.getItem("user");
      const token = localStorage.getItem("token");
      
      // Check for valid token and user data
      if (!token) {
        navigate("/login");
        setIsLoading(false);
        return;
      }
      
      // Validate user data before parsing
      if (!savedUser || savedUser === "undefined" || savedUser === "null") {
        // Token exists but user data is missing/corrupted, clean up and redirect
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        setIsLoading(false);
        return;
      }
      
      try {
        const parsedUser = JSON.parse(savedUser);
        setUser(parsedUser);
        
        // Check user status and redirect accordingly
        switch (parsedUser.status) {
          case "PENDING":
            navigate("/activation", { state: { email: parsedUser.email, pending: true } });
            return;
          case "EMAIL_VERIFIED":
            navigate("/contact-info");
            return;
          case "INFO_SUBMITTED":
            navigate("/pending-approval");
            return;
          case "ACTIVE":
            // Allow access to dashboard
            break;
          default:
            // Unknown status, log out
            localStorage.removeItem("token");
            localStorage.removeItem("user");
            navigate("/login");
            return;
        }
      } catch (e) {
        console.error("Failed to parse user data:", e);
        // Clean up invalid data
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
      } finally {
        setIsLoading(false);
      }
    };
    
    checkAuth();
  }, [navigate]);

  const handleLogout = () => {
    localStorage.removeItem("token");
    localStorage.removeItem("user");
    navigate("/");
  };

  // Show loading state while checking authentication
  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <div className="flex flex-col items-center gap-4">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
          <p className="text-sm text-muted-foreground">Loading...</p>
        </div>
      </div>
    );
  }

  if (!user) return null;

  return (
    <div className="flex h-screen bg-background overflow-hidden text-foreground">
      <DashboardSidebar />
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Top Header */}
        <header className="h-16 border-b border-border bg-card/50 backdrop-blur-sm flex items-center justify-between px-8 shrink-0 z-30">
          <div className="text-sm font-medium text-muted-foreground">
            {new Date().toLocaleDateString(undefined, { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' })}
          </div>
          <div className="flex items-center gap-4">
            <LanguageSwitcher />
            <button className="p-2 text-muted-foreground hover:text-foreground transition-colors relative">
              <Bell size={20} />
              <span className="absolute top-1.5 right-1.5 w-2 h-2 bg-primary rounded-full border-2 border-card" />
            </button>
            <div className="h-8 w-px bg-border mx-2" />
            <div className="flex items-center gap-3">
              <div className="text-right hidden sm:block">
                <div className="text-sm font-semibold truncate max-w-[150px]">{user.email}</div>

              </div>
              <div className="w-10 h-10 rounded-full bg-secondary flex items-center justify-center border border-border">
                <User size={20} className="text-muted-foreground" />
              </div>
              <Button variant="ghost" size="icon" onClick={handleLogout} className="text-muted-foreground">
                <LogOut size={20} />
              </Button>
            </div>
          </div>
        </header>

        {/* Page Content */}
        <main className="flex-1 overflow-y-auto p-8 bg-background/50">
          <Outlet />
        </main>
      </div>
    </div>
  );
};

export default DashboardLayout;
