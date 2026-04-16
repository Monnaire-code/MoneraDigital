import { useTranslation } from "react-i18next";
import { Mail, Clock, ExternalLink } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";

const Deposit = () => {
  const { t } = useTranslation();

  return (
    <div className="space-y-6 animate-fade-in">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">{t("deposit.title")}</h1>
        <p className="text-muted-foreground mt-2">{t("deposit.description")}</p>
      </div>
      
      <div className="max-w-2xl mx-auto">
        <Card className="border-border/50 bg-card/50 backdrop-blur-sm">
          <CardHeader className="text-center pb-2">
            <div className="mx-auto mb-4 p-4 bg-primary/10 rounded-full w-fit">
              <Clock className="w-12 h-12 text-primary" />
            </div>
            <CardTitle className="text-2xl">{t("deposit.comingSoon.title")}</CardTitle>
            <CardDescription className="text-base mt-2">
              {t("deposit.comingSoon.description")}
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6 pt-4">
            <div className="bg-muted/30 rounded-lg p-6 space-y-4">
              <h3 className="font-semibold text-lg">{t("deposit.comingSoon.howToDeposit")}</h3>
              <div className="space-y-3">
                <div className="flex items-start gap-3">
                  <div className="w-6 h-6 rounded-full bg-primary/20 text-primary flex items-center justify-center text-sm font-bold shrink-0">1</div>
                  <div>
                    <p className="font-medium">{t("deposit.comingSoon.step1.title")}</p>
                    <p className="text-sm text-muted-foreground">{t("deposit.comingSoon.step1.desc")}</p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <div className="w-6 h-6 rounded-full bg-primary/20 text-primary flex items-center justify-center text-sm font-bold shrink-0">2</div>
                  <div>
                    <p className="font-medium">{t("deposit.comingSoon.step2.title")}</p>
                    <p className="text-sm text-muted-foreground">{t("deposit.comingSoon.step2.desc")}</p>
                  </div>
                </div>
                <div className="flex items-start gap-3">
                  <div className="w-6 h-6 rounded-full bg-primary/20 text-primary flex items-center justify-center text-sm font-bold shrink-0">3</div>
                  <div>
                    <p className="font-medium">{t("deposit.comingSoon.step3.title")}</p>
                    <p className="text-sm text-muted-foreground">{t("deposit.comingSoon.step3.desc")}</p>
                  </div>
                </div>
              </div>
            </div>

            <div className="bg-gradient-to-br from-primary/10 to-primary/5 rounded-lg p-6 border border-primary/20">
              <h3 className="font-semibold text-lg mb-4 flex items-center gap-2">
                <Mail className="w-5 h-5" />
                {t("deposit.comingSoon.contactUs")}
              </h3>
              <p className="text-muted-foreground mb-4">
                {t("deposit.comingSoon.contactDesc")}
              </p>
              <div className="flex items-center justify-center gap-3 p-4 bg-background/80 rounded-lg border">
                <Mail className="w-5 h-5 text-primary" />
                <a 
                  href="mailto:ops@moneradigital.com" 
                  className="text-primary font-semibold text-lg hover:underline flex items-center gap-2"
                >
                  ops@moneradigital.com
                  <ExternalLink className="w-4 h-4" />
                </a>
              </div>
            </div>

            <div>
              <h3 className="font-semibold mb-3">{t("deposit.comingSoon.supportedCoins")}</h3>
              <div className="flex flex-wrap gap-2">
                {["USDT", "USDC", "BTC", "ETH"].map((coin) => (
                  <span 
                    key={coin} 
                    className="px-3 py-1.5 bg-secondary/50 rounded-full text-sm font-medium border"
                  >
                    {coin}
                  </span>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
};

export default Deposit;
