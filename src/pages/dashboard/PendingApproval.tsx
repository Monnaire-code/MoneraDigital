import { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Loader2, CheckCircle2, XCircle, Clock, Mail, Phone, MessageSquare, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import AuthNavbar from "@/components/AuthNavbar";

interface UserContactInfo {
  email: string;
  phone?: string;
  telegram?: string;
  wechat?: string;
  status?: string;
}

const PendingApproval = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [userInfo, setUserInfo] = useState<UserContactInfo | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isChecking, setIsChecking] = useState(false);

  const fetchUserInfo = async () => {
    try {
      const token = localStorage.getItem("token");
      const res = await fetch("/api/auth/me", {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        setUserInfo({
          email: data.email || "",
          phone: data.phone || "",
          telegram: data.telegram || "",
          wechat: data.wechat || "",
          status: data.status || "",
        });

        switch (data.status) {
          case "ACTIVE":
            toast.success(t("pendingApproval.accountActivated"));
            localStorage.setItem("user", JSON.stringify({ ...data, status: "ACTIVE" }));
            navigate("/");
            break;
          case "EMAIL_VERIFIED":
            navigate("/contact-info");
            break;
          case "PENDING":
            navigate("/activation");
            break;
          default:
            break;
        }
      }
    } catch (error) {
      console.error("Failed to fetch user info:", error);
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    fetchUserInfo();
  }, []);

  const handleRefresh = async () => {
    setIsChecking(true);
    await fetchUserInfo();
    setIsChecking(false);
  };

  const handleReSubmit = () => {
    navigate("/contact-info");
  };

  const getExpectedReviewDate = () => {
    const date = new Date();
    date.setDate(date.getDate() + 3);
    return date.toLocaleDateString("zh-CN", {
      year: "numeric",
      month: "long",
      day: "numeric",
    });
  };

  const maskPhone = (phone: string) => {
    if (!phone || phone.length < 7) return phone;
    const visibleDigits = 4;
    const startVisible = phone.length - visibleDigits;
    return phone.slice(0, 3) + "****" + phone.slice(startVisible);
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-[60vh]">
        <Loader2 className="h-8 w-8 animate-spin text-primary" />
      </div>
    );
  }

  const isRejected = userInfo?.status === "DISABLED";
  const isReviewInProgress = userInfo?.status === "INFO_SUBMITTED";

  return (
    <div className="min-h-screen bg-background">
      <AuthNavbar />
      <div className="flex items-center justify-center min-h-screen pt-20 pb-8 relative overflow-hidden">
        <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
        <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
        <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />
        <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />

        <Card className="w-full max-w-md relative z-10 backdrop-blur-sm bg-card/80">
          {isReviewInProgress ? (
            <>
              <CardHeader className="text-center">
                <div className="mx-auto mb-4 w-16 h-16 rounded-full bg-green-100 flex items-center justify-center">
                  <CheckCircle2 className="w-8 h-8 text-green-600" />
                </div>
                <CardTitle className="text-2xl">{t("pendingApproval.title")}</CardTitle>
                <CardDescription>{t("pendingApproval.description")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-6">
                <div className="bg-blue-50 border border-blue-200 rounded-lg p-4 space-y-2">
                  <div className="flex items-center gap-2 text-blue-800">
                    <Clock className="w-5 h-5" />
                    <span className="font-medium">{t("pendingApproval.expectedReviewTime")}</span>
                  </div>
                  <p className="text-blue-700 text-sm">
                    {t("pendingApproval.expectedReviewDate", { date: getExpectedReviewDate() })}
                  </p>
                </div>

                <div className="border rounded-lg p-4">
                  <h3 className="font-medium mb-3">{t("pendingApproval.submittedInfo")}</h3>
                  <div className="space-y-3">
                    <div className="flex items-center gap-3">
                      <Mail className="w-4 h-4 text-muted-foreground" />
                      <div className="flex-1">
                        <Label className="text-xs text-muted-foreground">{t("pendingApproval.email")}</Label>
                        <p className="text-sm font-medium">{userInfo?.email}</p>
                      </div>
                    </div>

                    {userInfo?.phone && (
                      <div className="flex items-center gap-3">
                        <Phone className="w-4 h-4 text-muted-foreground" />
                        <div className="flex-1">
                          <Label className="text-xs text-muted-foreground">{t("pendingApproval.phone")}</Label>
                          <p className="text-sm font-medium">{maskPhone(userInfo.phone)}</p>
                        </div>
                      </div>
                    )}

                    {userInfo?.telegram && (
                      <div className="flex items-center gap-3">
                        <MessageSquare className="w-4 h-4 text-muted-foreground" />
                        <div className="flex-1">
                          <Label className="text-xs text-muted-foreground">{t("pendingApproval.telegram")}</Label>
                          <p className="text-sm font-medium">{userInfo.telegram}</p>
                        </div>
                      </div>
                    )}

                    {userInfo?.wechat && (
                      <div className="flex items-center gap-3">
                        <svg className="w-4 h-4 text-muted-foreground" viewBox="0 0 24 24" fill="currentColor">
                          <path d="M8.691 2.188C3.891 2.188 0 5.476 0 9.53c0 2.212 1.17 4.203 3.002 5.55a.59.59 0 0 1 .213.665l-.39 1.48c-.019.07-.048.141-.048.213 0 .163.13.295.29.295a.326.326 0 0 0 .167-.054l1.903-1.114a.864.864 0 0 1 .717-.098 10.16 10.16 0 0 0 2.837.403c.276 0 .543-.027.811-.05-.857-2.578.157-4.972 1.932-6.446 1.703-1.415 3.882-1.98 5.853-1.838-.576-3.583-4.196-6.348-8.596-6.348z"/>
                        </svg>
                        <div className="flex-1">
                          <Label className="text-xs text-muted-foreground">{t("pendingApproval.wechat")}</Label>
                          <p className="text-sm font-medium">{userInfo.wechat}</p>
                        </div>
                      </div>
                    )}
                  </div>
                </div>

                <div className="bg-gray-50 border rounded-lg p-4">
                  <p className="text-sm text-muted-foreground">
                    {t("pendingApproval.needHelp")}{" "}
                    <a href="mailto:ops@moneradigital.com" className="text-primary hover:underline">
                      ops@moneradigital.com
                    </a>
                  </p>
                </div>

                <div className="flex justify-center">
                  <Button variant="outline" onClick={handleRefresh} disabled={isChecking}>
                    {isChecking ? (
                      <>
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        {t("pendingApproval.checking")}
                      </>
                    ) : (
                      <>
                        <RefreshCw className="mr-2 h-4 w-4" />
                        {t("pendingApproval.refresh")}
                      </>
                    )}
                  </Button>
                </div>
              </CardContent>
            </>
          ) : isRejected ? (
            <>
              <CardHeader className="text-center">
                <div className="mx-auto mb-4 w-16 h-16 rounded-full bg-red-100 flex items-center justify-center">
                  <XCircle className="w-8 h-8 text-red-600" />
                </div>
                <CardTitle className="text-2xl">{t("pendingApproval.accountRejected")}</CardTitle>
                <CardDescription>{t("pendingApproval.rejectedMessage")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-6">
                <div className="bg-red-50 border border-red-200 rounded-lg p-4 space-y-2">
                  <div className="flex items-center gap-2 text-red-800">
                    <Clock className="w-5 h-5" />
                    <span className="font-medium">{t("pendingApproval.reviewInProgress")}</span>
                  </div>
                </div>

                <div className="bg-gray-50 border rounded-lg p-4">
                  <p className="text-sm text-muted-foreground">
                    {t("pendingApproval.needHelp")}{" "}
                    <a href="mailto:ops@moneradigital.com" className="text-primary hover:underline">
                      ops@moneradigital.com
                    </a>
                  </p>
                </div>

                <div className="flex justify-center gap-4">
                  <Button variant="outline" onClick={handleRefresh} disabled={isChecking}>
                    {isChecking ? (
                      <>
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        {t("pendingApproval.checking")}
                      </>
                    ) : (
                      <>
                        <RefreshCw className="mr-2 h-4 w-4" />
                        {t("pendingApproval.refresh")}
                      </>
                    )}
                  </Button>
                  <Button onClick={handleReSubmit}>
                    {t("pendingApproval.reSubmit")}
                  </Button>
                </div>
              </CardContent>
            </>
          ) : (
            <>
              <CardHeader className="text-center">
                <div className="mx-auto mb-4 w-16 h-16 rounded-full bg-blue-100 flex items-center justify-center">
                  <Clock className="w-8 h-8 text-blue-600" />
                </div>
                <CardTitle className="text-2xl">{t("pendingApproval.reviewInProgress")}</CardTitle>
                <CardDescription>{t("pendingApproval.description")}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-6">
                <div className="bg-gray-50 border rounded-lg p-4">
                  <p className="text-sm text-muted-foreground">
                    {t("pendingApproval.needHelp")}{" "}
                    <a href="mailto:ops@moneradigital.com" className="text-primary hover:underline">
                      ops@moneradigital.com
                    </a>
                  </p>
                </div>

                <div className="flex justify-center">
                  <Button variant="outline" onClick={handleRefresh} disabled={isChecking}>
                    {isChecking ? (
                      <>
                        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                        {t("pendingApproval.checking")}
                      </>
                    ) : (
                      <>
                        <RefreshCw className="mr-2 h-4 w-4" />
                        {t("pendingApproval.refresh")}
                      </>
                    )}
                  </Button>
                </div>
              </CardContent>
            </>
          )}
        </Card>
      </div>
    </div>
  );
};

export default PendingApproval;
