import { useState, useEffect } from "react";
import { useNavigate, Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";

interface RegisterError {
  error?: string;
  code: string;
  message: string;
  details?: string;
}

export default function Register() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [emailError, setEmailError] = useState("");
  const [passwordError, setPasswordError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const navigate = useNavigate();
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

  const getLocalizedError = (message: string) => {
    if (!message) return "";
    const msg = message.toLowerCase();
    
    if (msg.includes("email is required")) return t("auth.errors.emailRequired");
    if (msg.includes("invalid email")) return t("auth.errors.invalidEmailFormat");
    if (msg.includes("password is required")) return t("auth.errors.passwordRequired");
    if (msg.includes("at least 8 characters")) return t("auth.errors.passwordTooShort");
    if (msg.includes("uppercase, lowercase, and digit")) return t("auth.errors.passwordComplexity");
    if (msg.includes("email is already registered") || msg.includes("user already exists") || msg.includes("email already exists")) return t("auth.errors.userAlreadyExists");
    
    return message;
  };

  const isValidEmail = (email: string) => {
    const emailRegex = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
    return emailRegex.test(email);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    clearErrors();
    setIsLoading(true);
    console.log("Attempting registration for:", email);

    if (password.length < 8) {
      setPasswordError(t("auth.errors.passwordTooShort"));
      setIsLoading(false);
      return;
    }

    if (!isValidEmail(email)) {
      setEmailError(t("auth.errors.invalidEmailFormat"));
      setIsLoading(false);
      return;
    }

    try {
      const res = await fetch("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });

      let data: RegisterError;
      const contentType = res.headers.get("content-type");
      if (contentType && contentType.includes("application/json")) {
        data = await res.json();
      } else {
        const text = await res.text();
        console.error("Non-JSON response received:", text);
        throw new Error(t("auth.errors.registrationFailed"));
      }

      if (!res.ok) {
        handleApiError(data);
        return;
      }

      console.log("Registration successful");
      toast.success(t("auth.register.successMessage"));
      
      setTimeout(() => {
        navigate("/login");
      }, 1000);
    } catch (error: any) {
      console.error("Registration error:", error);
      if (!emailError && !passwordError) {
        toast.error(getLocalizedError(error.message));
      }
    } finally {
      setIsLoading(false);
    }
  };

  const handleApiError = (data: RegisterError) => {
    const errorMessage = data.error || "";
    const errorCode = data.code || "";
    const errorDetails = data.details || "";

    if (errorMessage.includes("email already registered") || errorCode === "EMAIL_ALREADY_EXISTS") {
      setEmailError(t("auth.errors.emailAlreadyRegistered"));
      return;
    }
    if (errorMessage.includes("email") && (errorMessage.includes("invalid") || errorMessage.includes("format"))) {
      setEmailError(t("auth.errors.invalidEmailFormat"));
      return;
    }
    if (errorMessage.includes("password") && (errorMessage.includes("invalid") || errorMessage.includes("format") || errorMessage.includes("too short"))) {
      setPasswordError(t("auth.errors.invalidPasswordFormat"));
      return;
    }
    if (errorMessage.includes("uppercase, lowercase, and digit") || errorMessage.includes("password must contain")) {
      setPasswordError(t("auth.errors.passwordComplexity"));
      return;
    }
    if (errorCode === "VALIDATION_ERROR") {
      if (errorDetails === "email") {
        setEmailError(t("auth.errors.invalidEmailFormat"));
        return;
      }
      if (errorDetails === "password") {
        setPasswordError(t("auth.errors.invalidPasswordFormat"));
        return;
      }
      toast.error(t("auth.errors.invalidParameters"));
      return;
    }
    if (errorCode === "INTERNAL_ERROR" || errorCode === "PANIC_RECOVERED") {
      toast.error(t("auth.errors.serverError"));
      return;
    }
    toast.error(getLocalizedError(errorMessage) || t("auth.errors.registrationFailed"));
  };

  return (
    <div className="flex items-center justify-center min-h-screen relative overflow-hidden">
      {/* Background Effects */}
      <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
      <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
      <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />
      {/* Bottom Gradient */}
      <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />
      
      {/* Register Card */}
      <Card className="w-full max-w-md relative z-10 backdrop-blur-sm bg-card/80">
        <CardHeader>
          <CardTitle>{t("auth.register.title")}</CardTitle>
          <CardDescription>{t("auth.register.description")}</CardDescription>
        </CardHeader>
        <form onSubmit={handleSubmit}>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="email" className={cn(emailError && "text-red-500")}>
                {t("auth.register.email")}
              </Label>
              <Input
                id="email"
                type="email"
                placeholder={t("auth.register.emailPlaceholder")}
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
                {t("auth.register.password")}
              </Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value);
                  if (passwordError) setPasswordError("");
                }}
                className={cn(passwordError && "border-red-500 focus-visible:ring-red-500")}
                required
              />
              <p className={cn(
                "text-sm flex items-center gap-1.5",
                passwordError ? "text-red-500" : "text-muted-foreground"
              )}>
                {passwordError ? (
                  <>
                    <span className="w-1 h-1 rounded-full bg-red-500"></span>
                    {passwordError}
                  </>
                ) : (
                  t("auth.register.passwordRequirements")
                )}
              </p>
            </div>
          </CardContent>
          <CardFooter className="flex flex-col space-y-4">
            <Button type="submit" className="w-full" disabled={isLoading}>
              {isLoading ? t("auth.register.registering") : t("auth.register.button")}
            </Button>
            <div className="text-sm text-center">
              {t("auth.register.hasAccount")}{" "}
              <Link to="/login" className="text-blue-600 hover:underline">
                {t("auth.register.login")}
              </Link>
            </div>
          </CardFooter>
        </form>
      </Card>
    </div>
  );
}
