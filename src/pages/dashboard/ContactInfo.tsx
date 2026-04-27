import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Phone, MessageSquare, AtSign, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { ContactService } from "@/lib/contact-service";
import { PhoneInput } from "react-international-phone";
import "react-international-phone/style.css";
import AuthNavbar from "@/components/AuthNavbar";

const ContactInfo = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [telegram, setTelegram] = useState("");
  const [wechat, setWechat] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [isFetchingUser, setIsFetchingUser] = useState(true);

  useEffect(() => {
    const fetchUserInfo = async () => {
      try {
        const token = localStorage.getItem("token");
        const res = await fetch("/api/auth/me", {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (res.ok) {
          const data = await res.json();
          setEmail(data.email || "");
        }
      } catch (error) {
        console.error("Failed to fetch user info:", error);
      } finally {
        setIsFetchingUser(false);
      }
    };
    fetchUserInfo();
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();

    if (!phone.trim()) {
      toast.error(t("contactInfo.errors.phoneRequired"));
      return;
    }

    setIsLoading(true);
    try {
      const result = await ContactService.submitContactInfo({
        phone,
        telegram: telegram.trim() || undefined,
        wechat: wechat.trim() || undefined,
      });

      if (result.success) {
        toast.success(t("contactInfo.success"));
        const userData = {
          id: JSON.parse(localStorage.getItem("user") || "{}").id,
          email,
          status: "INFO_SUBMITTED",
        };
        localStorage.setItem("user", JSON.stringify(userData));
        navigate("/pending-approval");
      } else {
        const errorKey = `contactInfo.errors.${result.code}` as const;
        const errorMessage = t(errorKey);
        toast.error(errorMessage !== errorKey ? errorMessage : (result.error || t("contactInfo.errors.submitFailed")));
      }
    } catch (error) {
      console.error("Submit contact info error:", error);
      toast.error(t("contactInfo.errors.submitFailed"));
    } finally {
      setIsLoading(false);
    }
  };

  if (isFetchingUser) {
    return (
      <div className="flex items-center justify-center h-[60vh]">
        <Loader2 className="h-8 w-8 animate-spin text-primary" />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background">
      <AuthNavbar />
      <div className="flex items-center justify-center min-h-screen pt-20 pb-8 relative overflow-hidden">
        <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
        <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
        <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />
        <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />

        <Card className="w-full max-w-md relative z-10 backdrop-blur-sm bg-card/80">
          <CardHeader>
            <CardTitle>{t("contactInfo.title")}</CardTitle>
            <CardDescription>{t("contactInfo.description")}</CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleSubmit} className="space-y-6">
              <div className="space-y-2">
                <Label htmlFor="email" className="flex items-center gap-2">
                  <AtSign className="h-4 w-4" />
                  {t("contactInfo.email")}
                </Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  readOnly
                  disabled
                  className="bg-muted cursor-not-allowed"
                />
                <p className="text-xs text-muted-foreground">
                  {t("contactInfo.emailReadonly")}
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="phone" className="flex items-center gap-2">
                  <Phone className="h-4 w-4" />
                  {t("contactInfo.phone")}
                  <span className="text-red-500">*</span>
                </Label>
                <PhoneInput
                  defaultCountry="us"
                  value={phone}
                  onChange={(phone) => setPhone(phone)}
                  className="phone-input"
                  inputClassName="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background file:border-0 file:bg-background file:text-sm file:font-medium placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                />
                <p className="text-xs text-muted-foreground">
                  {t("contactInfo.phoneHelp")}
                </p>
              </div>

              <div className="space-y-2">
                <Label htmlFor="telegram" className="flex items-center gap-2">
                  <MessageSquare className="h-4 w-4" />
                  {t("contactInfo.telegram")}
                  <span className="text-xs text-muted-foreground">({t("contactInfo.optional")})</span>
                </Label>
                <Input
                  id="telegram"
                  type="text"
                  value={telegram}
                  onChange={(e) => setTelegram(e.target.value)}
                  placeholder={t("contactInfo.telegramPlaceholder")}
                />
              </div>

              <div className="space-y-2">
                <Label htmlFor="wechat" className="flex items-center gap-2">
                  <svg className="h-4 w-4" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M8.691 2.188C3.891 2.188 0 5.476 0 9.53c0 2.212 1.17 4.203 3.002 5.55a.59.59 0 0 1 .213.665l-.39 1.48c-.019.07-.048.141-.048.213 0 .163.13.295.29.295a.326.326 0 0 0 .167-.054l1.903-1.114a.864.864 0 0 1 .717-.098 10.16 10.16 0 0 0 2.837.403c.276 0 .543-.027.811-.05-.857-2.578.157-4.972 1.932-6.446 1.703-1.415 3.882-1.98 5.853-1.838-.576-3.583-4.196-6.348-8.596-6.348zM5.785 5.991c.642 0 1.162.529 1.162 1.18a1.17 1.17 0 0 1-1.162 1.178A1.17 1.17 0 0 1 4.623 7.17c0-.651.52-1.18 1.162-1.18zm5.813 0c.642 0 1.162.529 1.162 1.18a1.17 1.17 0 0 1-1.162 1.178 1.17 1.17 0 0 1-1.162-1.178c0-.651.52-1.18 1.162-1.18zm5.34 2.867c-1.797-.052-3.746.512-5.28 1.786-1.72 1.428-2.687 3.72-1.78 6.22.942 2.453 3.666 4.229 6.884 4.229.826 0 1.622-.12 2.361-.336a.722.722 0 0 1 .598.082l1.584.926a.272.272 0 0 0 .14.047c.134 0 .24-.111.24-.247 0-.06-.023-.12-.038-.177l-.327-1.233a.582.582 0 0 1-.023-.156.49.49 0 0 1 .201-.398C23.024 18.48 24 16.82 24 14.98c0-3.21-2.931-5.837-6.656-6.088V8.89c-.135-.012-.269-.03-.407-.032zm-2.53 3.274c.535 0 .969.44.969.982a.976.976 0 0 1-.969.983.976.976 0 0 1-.969-.983c0-.542.434-.982.97-.982zm4.844 0c.535 0 .969.44.969.982a.976.976 0 0 1-.969.983.976.976 0 0 1-.969-.983c0-.542.434-.982.969-.982z"/>
                  </svg>
                  {t("contactInfo.wechat")}
                  <span className="text-xs text-muted-foreground">({t("contactInfo.optional")})</span>
                </Label>
                <Input
                  id="wechat"
                  type="text"
                  value={wechat}
                  onChange={(e) => setWechat(e.target.value)}
                  placeholder={t("contactInfo.wechatPlaceholder")}
                />
              </div>

              <Button type="submit" className="w-full" disabled={isLoading}>
                {isLoading ? (
                  <>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    {t("contactInfo.submitting")}
                  </>
                ) : (
                  t("contactInfo.submit")
                )}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default ContactInfo;
