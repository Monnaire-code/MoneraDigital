import { Button } from "@/components/ui/button";
import { ArrowRight, Shield, TrendingUp, Lock } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Link } from "react-router-dom";
import { useEffect, useState } from "react";
import HeroAumCard from "@/components/HeroAumCard";

const Hero = () => {
  const { t } = useTranslation();
  const [isLoggedIn, setIsLoggedIn] = useState(false);

  useEffect(() => {
    setIsLoggedIn(!!localStorage.getItem("token"));
  }, []);

  return (
    <section className="relative min-h-screen flex items-center justify-center overflow-hidden pt-20">
      {/* Background Effects */}
      <div className="absolute inset-0 bg-grid-pattern bg-[size:60px_60px] opacity-[0.03]" />
      <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-primary/10 rounded-full blur-[120px] animate-pulse-slow" />
      <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-primary/5 rounded-full blur-[100px] animate-pulse-slow" />

      <div className="container mx-auto px-6 relative z-10">
        <div className="max-w-4xl mx-auto text-center">
          {/* Badge */}
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full glass mb-8 animate-fade-in">
            <span className="w-2 h-2 rounded-full bg-primary animate-glow" />
            <span className="text-sm text-muted-foreground">
              {t("hero.badge")}
            </span>
          </div>

          {/* Headline */}
          <h1 className="text-4xl sm:text-5xl lg:text-7xl font-bold tracking-tight mb-6 animate-fade-in-up">
            <span className="text-foreground">{t("hero.headline")}</span>
            <br />
            <span className="text-primary">{t("hero.headlineHighlight")}</span>
          </h1>

          {/* Subheadline */}
          <p className="text-lg sm:text-xl text-muted-foreground max-w-2xl mx-auto mb-10 animate-fade-in-delay-1">
            {t("hero.subheadline")}
          </p>

          {/* CTA Buttons */}
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mb-16 animate-fade-in-delay-2">
            <Button variant="hero" size="xl" asChild>
              <Link to={isLoggedIn ? "/dashboard" : "/register"}>
                {isLoggedIn ? (t("hero.buttons.dashboard") || "Go to Dashboard") : t("hero.buttons.startEarning")}
                <ArrowRight className="ml-2" />
              </Link>
            </Button>
            <Button variant="heroOutline" size="xl" asChild>
              <a href="#products">
                {t("hero.buttons.exploreProducts")}
              </a>
            </Button>
          </div>

          {/* Trust Indicators */}
          <HeroAumCard />
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-6 max-w-3xl mx-auto animate-fade-in-delay-3">
            <div className="flex items-center justify-center gap-3 p-4 rounded-xl glass">
              <Shield className="w-5 h-5 text-primary" />
              <span className="text-sm text-muted-foreground">{t("hero.trustIndicators.coldStorage")}</span>
            </div>
            <div className="flex items-center justify-center gap-3 p-4 rounded-xl glass">
              <TrendingUp className="w-5 h-5 text-primary" />
              <span className="text-sm text-muted-foreground">{t("hero.trustIndicators.apy")}</span>
            </div>
            <div className="flex items-center justify-center gap-3 p-4 rounded-xl glass">
              <Lock className="w-5 h-5 text-primary" />
              <span className="text-sm text-muted-foreground">{t("hero.trustIndicators.segregation")}</span>
            </div>
          </div>
        </div>
      </div>

      {/* Bottom Gradient */}
      <div className="absolute bottom-0 left-0 right-0 h-32 bg-gradient-to-t from-background to-transparent" />
    </section>
  );
};

export default Hero;
