import { useState, useEffect, useRef } from "react";
import { useNavigate, Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { ActivationService } from "@/lib/activation-service";
import { cn } from "@/lib/utils";

export default function Activation() {
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [codeError, setCodeError] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [isSending, setIsSending] = useState(false);
  const [countdown, setCountdown] = useState(0);
  const [isActivated, setIsActivated] = useState(false);
  const navigate = useNavigate();
  const { t } = useTranslation();
  const inputRefs = useRef<(HTMLInputElement | null)[]>([]);

  useEffect(() => {
    const savedEmail = localStorage.getItem("pendingActivationEmail");
    if (savedEmail) {
      setEmail(savedEmail);
    }

    const userData = localStorage.getItem("user");
    if (userData) {
      try {
        const user = JSON.parse(userData);
        if (user.email) {
          setEmail(user.email);
          localStorage.setItem("pendingActivationEmail", user.email);
        }
      } catch {
        // ignore parse error
      }
    }
  }, []);

  useEffect(() => {
    if (countdown > 0) {
      const timer = setTimeout(() => setCountdown(countdown - 1), 1000);
      return () => clearTimeout(timer);
    }
  }, [countdown]);

  useEffect(() => {
    if (isActivated) {
      const timer = setTimeout(() => {
        navigate("/");
      }, 2000);
      return () => clearTimeout(timer);
    }
  }, [isActivated, navigate]);

  const handleCodeChange = (index: number, value: string) => {
    if (!/^\d*$/.test(value)) return;

    const newCode = code.split("");
    newCode[index] = value.slice(-1);
    const updatedCode = newCode.join("").slice(0, 6);
    setCode(updatedCode);
    setCodeError("");

    if (value && index < 5) {
      inputRefs.current[index + 1]?.focus();
    }
  };

  const handleKeyDown = (index: number, e: React.KeyboardEvent) => {
    if (e.key === "Backspace" && !code[index] && index > 0) {
      inputRefs.current[index - 1]?.focus();
    }
  };

  const handlePaste = (e: React.ClipboardEvent) => {
    e.preventDefault();
    const pastedData = e.clipboardData.getData("text").replace(/\D/g, "").slice(0, 6);
    setCode(pastedData);
    setCodeError("");

    pastedData.split("").forEach((char, index) => {
      if (inputRefs.current[index]) {
        inputRefs.current[index]!.value = char;
      }
    });

    if (pastedData.length === 6) {
      inputRefs.current[5]?.focus();
    } else if (pastedData.length > 0) {
      inputRefs.current[pastedData.length]?.focus();
    }
  };

  const handleSendCode = async () => {
    if (!email) {
      toast.error(t("activation.errors.emailRequired"));
      return;
    }

    setIsSending(true);
    try {
      const result = await ActivationService.sendActivationCode(email);
      if (result.success) {
        toast.success(t("activation.successCodeSent"));
        setCountdown(60);
      } else {
        if (result.retryAfter) {
          setCountdown(result.retryAfter);
          toast.error(t("activation.errors.tooManyRequests", { seconds: result.retryAfter }));
        } else {
          toast.error(result.message);
        }
      }
    } catch (error: any) {
      toast.error(error.message || t("activation.errors.sendFailed"));
    } finally {
      setIsSending(false);
    }
  };

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault();
    setCodeError("");

    if (code.length !== 6) {
      setCodeError(t("activation.errors.codeRequired"));
      return;
    }

    setIsLoading(true);
    try {
      const result = await ActivationService.verifyActivationCode(email, code);
      if (result.success) {
        setIsActivated(true);
        toast.success(t("activation.success"));
        localStorage.removeItem("pendingActivationEmail");
        if (result.userId) {
          const userData = {
            id: result.userId,
            email: email,
            status: "ACTIVE",
            twoFactorEnabled: false,
          };
          localStorage.setItem("user", JSON.stringify(userData));
        }
      }
    } catch (error: any) {
      const errorCode = error.code || error.message;
      switch (errorCode) {
        case "INVALID_CODE":
        case "CODE_INVALID":
          setCodeError(t("activation.errors.invalidCode"));
          break;
        case "CODE_EXPIRED":
          setCodeError(t("activation.errors.codeExpired"));
          break;
        case "MAX_ATTEMPTS":
        case "MAX_ATTEMPTS_EXCEEDED":
          setCodeError(t("activation.errors.maxAttempts"));
          handleSendCode();
          break;
        default:
          setCodeError(error.message || t("activation.errors.verifyFailed"));
      }
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-center min-h-screen relative overflow-hidden">
      <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
      <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
      <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />
      <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />

      <Card className="w-full max-w-md relative z-10 backdrop-blur-sm bg-card/80">
        <CardHeader>
          <CardTitle>{t("activation.title")}</CardTitle>
          <CardDescription>{t("activation.description")}</CardDescription>
        </CardHeader>

        {isActivated ? (
          <CardContent className="text-center py-8">
            <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-green-100 flex items-center justify-center">
              <svg className="w-8 h-8 text-green-600" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
              </svg>
            </div>
            <p className="text-lg font-medium text-green-600">{t("activation.success")}</p>
            <p className="text-sm text-muted-foreground mt-2">{t("activation.redirecting")}</p>
          </CardContent>
        ) : (
          <form onSubmit={handleVerify}>
            <CardContent className="space-y-6">
              <div className="space-y-2">
                <Label htmlFor="email">{t("activation.emailLabel")}</Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder={t("activation.emailPlaceholder")}
                  disabled={isActivated}
                />
              </div>

              <div className="space-y-3">
                <Label>{t("activation.codeLabel")}</Label>
                <div className="flex gap-2 justify-center" onPaste={handlePaste}>
                  {[0, 1, 2, 3, 4, 5].map((index) => (
                    <Input
                      key={index}
                      ref={(el) => { inputRefs.current[index] = el; }}
                      type="text"
                      inputMode="numeric"
                      maxLength={1}
                      value={code[index] || ""}
                      onChange={(e) => handleCodeChange(index, e.target.value)}
                      onKeyDown={(e) => handleKeyDown(index, e)}
                      className={cn(
                        "w-12 h-14 text-center text-xl font-bold",
                        codeError && "border-red-500"
                      )}
                      disabled={isLoading}
                    />
                  ))}
                </div>
                {codeError && (
                  <p className="text-sm text-red-500 text-center">{codeError}</p>
                )}
              </div>

              <div className="flex justify-between items-center text-sm">
                <Button
                  type="button"
                  variant="link"
                  onClick={handleSendCode}
                  disabled={isSending || countdown > 0}
                  className="p-0 h-auto"
                >
                  {countdown > 0
                    ? t("activation.resendCountdown", { seconds: countdown })
                    : t("activation.resendButton")}
                </Button>
              </div>
            </CardContent>

            <CardFooter className="flex flex-col space-y-4">
              <Button type="submit" className="w-full" disabled={isLoading || code.length !== 6}>
                {isLoading ? t("activation.verifying") : t("activation.verifyButton")}
              </Button>
              <div className="text-sm text-center">
                <Link to="/login" className="text-blue-600 hover:underline">
                  {t("activation.backToLogin")}
                </Link>
              </div>
            </CardFooter>
          </form>
        )}
      </Card>
    </div>
  );
}
