import { useState, useEffect } from "react";
import { useNavigate, useLocation, Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { validateRedirectPath } from "@/lib/redirect-validator";
import { cn } from "@/lib/utils";
import { Eye, EyeOff } from "lucide-react";

export default function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [emailError, setEmailError] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { t } = useTranslation();

  useEffect(() => {
    if (localStorage.getItem("token")) {
      navigate("/");
    }
  }, [navigate]);

  const clearErrors = () => {
    setEmailError("");
    setPasswordError("");
  };

  const isValidEmail = (email: string) => {
    const emailRegex = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
    return emailRegex.test(email);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    clearErrors();
    setIsLoading(true);

    if (!isValidEmail(email)) {
      setEmailError(t("auth.errors.invalidEmailFormat"));
      setIsLoading(false);
      return;
    }

    if (password.length < 1) {
      setPasswordError(t("auth.errors.passwordRequired"));
      setIsLoading(false);
      return;
    }

    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });

      const data = await res.json();
      if (!res.ok) {
        const errorMessage = data.error || "";

        if (errorMessage.includes("invalid credentials") || errorMessage.includes("invalid email") || errorMessage.includes("password")) {
          setEmailError(t("auth.errors.invalidEmailOrPassword"));
          setPasswordError(t("auth.errors.invalidEmailOrPassword"));
        } else if (errorMessage.includes("disabled")) {
          toast.error(t("auth.errors.accountDisabled"));
        } else {
          toast.error(t("auth.errors.serverError"));
        }
        setIsLoading(false);
        return;
      }

      // Login successful - store token and redirect
      localStorage.setItem("token", data.accessToken || data.token);
      // Only store user data if it exists to avoid "undefined" string in localStorage
      if (data.user) {
        localStorage.setItem("user", JSON.stringify(data.user));
      }

      // Check if user needs activation
      if (data.requiresActivation) {
        localStorage.setItem("pendingActivationEmail", email);
        toast.warning(t("auth.errors.accountNotActivated"));
        navigate("/activation", { state: { email, pending: true } });
        return;
      }

      toast.success(t("auth.login.successMessage"));
      
      // Redirect to validated returnTo or default to /dashboard
      const returnTo = validateRedirectPath((location.state as any)?.returnTo);
      navigate(returnTo);
    } catch (error: any) {
      console.error("Login error:", error);
      if (!emailError && !passwordError) {
        toast.error(t("auth.errors.serverError"));
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-center min-h-screen relative overflow-hidden">
      {/* Background Effects */}
      <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
      <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
      <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />
      {/* Bottom Gradient */}
      <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />
      
      {/* Login Card */}
      <Card className="w-full max-w-md relative z-10 backdrop-blur-sm bg-card/80">
        <CardHeader>
          <CardTitle>{t("auth.login.title")}</CardTitle>
          <CardDescription>{t("auth.login.description")}</CardDescription>
        </CardHeader>
        <form onSubmit={handleSubmit}>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="email" className={cn(emailError && "text-red-500")}>
                {t("auth.login.email")}
              </Label>
              <Input
                id="email"
                type="email"
                placeholder={t("auth.login.emailPlaceholder")}
                value={email}
                onChange={(e) => {
                  setEmail(e.target.value);
                  if (emailError) setEmailError("");
                }}
                onInvalid={(e) => {
                  e.preventDefault();
                  if (!isValidEmail(email)) {
                    setEmailError(t("auth.errors.invalidEmailFormat"));
                  }
                }}
                className={cn(emailError && "border-red-500 focus-visible:ring-red-500")}
              />
              {emailError && (
                <p className="text-sm text-red-500 flex items-center gap-1.5">
                  <span className="w-1 h-1 rounded-full bg-red-500"></span>
                  {emailError}
                </p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="password" className={cn(passwordError && "text-red-500")}>
                {t("auth.login.password")}
              </Label>
              <div className="relative">
                <Input
                  id="password"
                  type={showPassword ? "text" : "password"}
                  value={password}
                  onChange={(e) => {
                    setPassword(e.target.value);
                    if (passwordError) setPasswordError("");
                  }}
                  className={cn(passwordError && "border-red-500 focus-visible:ring-red-500", "pr-10")}
                />
                <button
                  type="button"
                  onClick={() => setShowPassword(!showPassword)}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground transition-colors"
                  tabIndex={-1}
                >
                  {showPassword ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </button>
              </div>
              {passwordError && (
                <p className="text-sm text-red-500 flex items-center gap-1.5">
                  <span className="w-1 h-1 rounded-full bg-red-500"></span>
                  {passwordError}
                </p>
              )}
            </div>
          </CardContent>
          <CardFooter className="flex flex-col space-y-4">
            <Button type="submit" className="w-full" disabled={isLoading}>
              {isLoading ? t("auth.login.loggingIn") : t("auth.login.button")}
            </Button>
            <div className="text-sm text-center">
              {t("auth.login.noAccount")}{" "}
              <Link to="/register" className="text-blue-600 hover:underline">
                {t("auth.login.register")}
              </Link>
            </div>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}
