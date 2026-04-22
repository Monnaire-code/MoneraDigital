import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { motion } from "framer-motion";
import { ChevronRight, Shield, Sparkles, Star, Calendar, Wallet, ArrowLeft, Globe, User, LogOut } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import Footer from "@/components/Footer";

const coinLogos: Record<string, { bg: string; text: string; symbol: string }> = {
  BTC: { bg: "bg-orange-500", text: "text-white", symbol: "₿" },
  ETH: { bg: "bg-indigo-500", text: "text-white", symbol: "Ξ" },
  USDT: { bg: "bg-emerald-500", text: "text-white", symbol: "$" },
  USDC: { bg: "bg-blue-500", text: "text-white", symbol: "$" },
};

const CoinIcon = ({ coin }: { coin: string }) => {
  const logo = coinLogos[coin] || { bg: "bg-gray-500", text: "text-white", symbol: coin[0] };
  return (
    <div className={`w-5 h-5 ${logo.bg} rounded-full flex items-center justify-center ${logo.text} text-[10px] font-bold`}>
      {logo.symbol}
    </div>
  );
};

const StructuredProducts = () => {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [mounted, setMounted] = useState(false);
  const [user, setUser] = useState<{ email: string } | null>(null);

  useEffect(() => {
    const savedUser = localStorage.getItem("user");
    if (savedUser && savedUser !== "undefined" && savedUser !== "null") {
      try {
        setUser(JSON.parse(savedUser));
      } catch (e) {
        localStorage.removeItem("user");
      }
    }
  }, []);

  const handleLogout = () => {
    localStorage.removeItem("token");
    localStorage.removeItem("user");
    setUser(null);
    navigate("/");
  };

  useEffect(() => {
    setMounted(true);
    window.scrollTo(0, 0);
  }, []);

  // Tech network particle background
  useEffect(() => {
    if (!mounted || !canvasRef.current) return;

    const canvas = canvasRef.current;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let animationId: number;
    const particles: Particle[] = [];
    const particleCount = 100;
    const connectionDistance = 120;

    class Particle {
      x: number;
      y: number;
      vx: number;
      vy: number;
      r: number;

      constructor() {
        this.x = Math.random() * canvas.width;
        this.y = Math.random() * canvas.height;
        this.vx = (Math.random() - 0.5) * 0.8;
        this.vy = (Math.random() - 0.5) * 0.8;
        this.r = Math.random() * 2 + 1;
      }

      update() {
        this.x += this.vx;
        this.y += this.vy;
        if (this.x < 0 || this.x > canvas.width) this.vx *= -1;
        if (this.y < 0 || this.y > canvas.height) this.vy *= -1;
      }

      draw() {
        ctx.beginPath();
        ctx.arc(this.x, this.y, this.r, 0, Math.PI * 2);
        ctx.fillStyle = "rgba(46,230,214,0.6)";
        ctx.fill();
      }
    }

    const resize = () => {
      canvas.width = window.innerWidth;
      canvas.height = window.innerHeight;
    };

    const draw = () => {
      ctx.clearRect(0, 0, canvas.width, canvas.height);

      // Draw connections
      for (let i = 0; i < particles.length; i++) {
        for (let j = i + 1; j < particles.length; j++) {
          const dx = particles[i].x - particles[j].x;
          const dy = particles[i].y - particles[j].y;
          const dist = Math.sqrt(dx * dx + dy * dy);

          if (dist < connectionDistance) {
            ctx.beginPath();
            ctx.moveTo(particles[i].x, particles[i].y);
            ctx.lineTo(particles[j].x, particles[j].y);
            ctx.strokeStyle = `rgba(46,230,214,${0.15 * (1 - dist / connectionDistance)})`;
            ctx.lineWidth = 0.5;
            ctx.stroke();
          }
        }
      }

      particles.forEach((p) => {
        p.update();
        p.draw();
      });

      animationId = requestAnimationFrame(draw);
    };

    const init = () => {
      resize();
      particles.length = 0;
      for (let i = 0; i < particleCount; i++) {
        particles.push(new Particle());
      }
    };

    init();
    draw();
    window.addEventListener("resize", resize);

    return () => {
      cancelAnimationFrame(animationId);
      window.removeEventListener("resize", resize);
    };
  }, [mounted]);

  const isZh = i18n.language === "zh";

  const termOptions = [
    { title: isZh ? "固定期限" : "Fixed Term", subtitle: isZh ? "选择1至12个月的投资期限" : "Choose 1-12 month investment term" },
    { title: isZh ? "开放期限" : "Open Term", subtitle: isZh ? "无固定投资期限，赎回产品需提前一周通知" : "No fixed investment term" },
  ];

  const assetOptions = [
    { category: isZh ? "稳定币" : "Stablecoin", apy: "8%", coins: ["USDT", "USDC"], isHighlight: true },
    { category: isZh ? "加密货币" : "Cryptocurrency", apy: "4%", coins: ["BTC", "ETH"], isHighlight: false },
  ];

  const steps = [
    { 
      number: "01", 
      title: isZh ? "选择期限" : "Select Terms", 
      description: isZh ? "选择您偏好的借出期限（固定期限或开放期限）" : "Choose your preferred lending term (fixed or open)" 
    },
    { 
      number: "02", 
      title: isZh ? "借出资产" : "Lend Assets", 
      description: isZh ? "将您的数字资产借出给 Monera Digital" : "Lend your digital assets to Monera Digital" 
    },
    { 
      number: "03", 
      title: isZh ? "获得利息" : "Earn Interest", 
      description: isZh ? "获得的利息将直接计入您的账户" : "Earned interest will be directly credited to your account" 
    },
  ];

  const benefits = [
    { title: isZh ? "值得信赖的平台" : "Trusted Platform", description: isZh ? "我们是加密资产财富管理的领先机构，提供专业级的解决方案。" : "Leading crypto asset management institution providing professional-grade solutions.", icon: <Star className="w-5 h-5" /> },
    { title: isZh ? "安全至上" : "Security First", description: isZh ? "采用机构级的托管和风险管理措施，保护您的资产安全。" : "Institutional-grade custody and risk management to protect your assets.", icon: <Shield className="w-5 h-5" /> },
    { title: isZh ? "透明的流程" : "Transparent Process", description: isZh ? "条款清晰，绝无隐藏费用。" : "Clear terms with no hidden fees.", icon: <Sparkles className="w-5 h-5" /> },
  ];

  return (
    <div className="relative min-h-screen bg-background text-foreground overflow-hidden">
      {/* Subtle Background Gradient */}
      <div className="fixed inset-0 bg-gradient-to-br from-primary/5 via-background to-background" />

      {/* Particle Canvas Background */}
      <canvas ref={canvasRef} className="fixed inset-0 w-full h-full opacity-20 pointer-events-none" />

      {/* Content */}
      <div className="relative z-10">
        {/* Navigation Header */}
        <header className="fixed top-0 left-0 right-0 z-50 glass">
          <div className="max-w-6xl mx-auto px-4 flex items-center justify-between h-16 lg:h-20">
            {/* Logo & Back */}
            <button 
              onClick={() => navigate("/")}
              className="flex items-center gap-3"
            >
              <img src="/m-logo-new.png" alt="Monera Digital" className="w-8 h-8 object-contain" />
              <span className="text-foreground font-semibold text-xl tracking-tight">
                Monera<span className="text-primary">Digital</span>
              </span>
            </button>
            
            {/* Right side - Language & Auth */}
            <div className="flex items-center gap-4">
              {/* Language Switcher */}
              <Button
                variant="ghost"
                size="sm"
                onClick={() => i18n.changeLanguage(i18n.language === "en" ? "zh" : "en")}
                className="flex items-center gap-2"
              >
                <Globe size={16} />
                <span className="font-medium">{i18n.language === "en" ? "中" : "EN"}</span>
              </Button>
              
              {/* Auth - Logged In */}
              {user ? (
                <div className="flex items-center gap-4">
                  <div className="flex items-center gap-2 text-sm font-medium text-foreground">
                    <User size={16} className="text-primary" />
                    <span>{user.email}</span>
                  </div>
                  <Button variant="ghost" size="sm" onClick={handleLogout} className="gap-2">
                    <LogOut size={16} />
                    {t("auth.login.logout")}
                  </Button>
                </div>
              ) : (
                /* Auth - Logged Out */
                <div className="flex items-center gap-4">
                  <Button variant="ghost" size="sm" onClick={() => navigate("/login")}>
                    {t("header.auth.signIn")}
                  </Button>
                  <Button variant="default" size="sm" onClick={() => navigate("/register")}>
                    {t("header.auth.getStarted")}
                  </Button>
                </div>
              )}
            </div>
          </div>
        </header>

        {/* Hero Section - Left Aligned */}
        <section className="max-w-6xl mx-auto px-4 pt-24 pb-16 lg:pt-28 lg:pb-20">
          <div className="max-w-3xl">
            <motion.div
              initial={{ opacity: 0, y: 30 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: 0.8 }}
            >
              {/* Title */}
              <h1 className="text-6xl sm:text-7xl lg:text-8xl font-extrabold tracking-tight mb-6 text-primary">
                {isZh ? "结构化产品" : "Vanilla Credit"}
              </h1>

              {/* Description */}
              <p className="text-muted-foreground text-lg sm:text-xl leading-relaxed mb-6">
                {isZh 
                  ? "通过加密货币借贷捕获具有竞争力的收益回报"
                  : "Capture competitive yields through cryptocurrency lending"
                }
              </p>
              <p className="text-muted-foreground/70 text-sm leading-relaxed whitespace-pre-line">
                {isZh 
                  ? `在 Monera Digital，我们为您提供一种简单且安全的投资方式，通过借出您的数字资产来获得额外收益。投资该产品，您不仅可以享受具有吸引力的利率，还能保持资金的高度流动性。`
                  : `At Monera Digital, we offer you a simple and secure way to invest by lending your digital assets to earn extra returns. With this product, you can enjoy attractive rates while maintaining high liquidity of your funds.`
                }
              </p>
            </motion.div>
          </div>
        </section>

        {/* Product Cards Section */}
        <section className="max-w-6xl mx-auto px-4 pb-20">
          <div className="grid md:grid-cols-2 gap-8">
            {/* Left - Flexible Terms */}
            <div>
              <h3 className="text-primary mb-6 text-lg font-semibold tracking-wide flex items-center gap-2">
                <Calendar className="w-5 h-5" />
                {isZh ? "灵活期限" : "Flexible Terms"}
              </h3>
              <div className="grid grid-cols-2 gap-4">
                {termOptions.map((item, i) => (
                  <motion.div
                    key={i}
                    whileHover={{ scale: 1.03 }}
                    transition={{ duration: 0.3 }}
                    className="bg-card border border-border rounded-2xl p-6 hover:border-primary/50 hover:shadow-[0_0_30px_rgba(var(--primary)/0.15)] transition-all duration-300 cursor-pointer h-[160px] flex flex-col justify-between"
                  >
                    <div>
                      <h4 className="text-card-foreground font-semibold mb-2">{item.title}</h4>
                      <p className="text-muted-foreground text-sm leading-relaxed">{item.subtitle}</p>
                    </div>
                  </motion.div>
                ))}
              </div>
            </div>

            {/* Right - Available Assets */}
            <div>
              <h3 className="text-primary mb-6 text-lg font-semibold tracking-wide flex items-center gap-2">
                <Wallet className="w-5 h-5" />
                {isZh ? "可用资产" : "Available Assets"}
              </h3>
              <div className="grid grid-cols-2 gap-4">
                {assetOptions.map((item, i) => (
                  <motion.div
                    key={i}
                    whileHover={{ scale: 1.03 }}
                    transition={{ duration: 0.3 }}
                    className={`
                      rounded-2xl p-6 relative overflow-hidden h-[160px] flex flex-col justify-between
                      ${item.isHighlight 
                        ? 'bg-gradient-to-br from-primary/15 to-card border border-primary/30 shadow-[0_0_30px_rgba(var(--primary)/0.2)]' 
                        : 'bg-card border border-border'
                      }
                      hover:border-primary/50 hover:shadow-[0_0_30px_rgba(var(--primary)/0.15)] transition-all duration-300 cursor-pointer
                    `}
                  >
                    {/* Glow effect for highlighted card */}
                    {item.isHighlight && (
                      <div className="absolute -top-10 -right-10 w-32 h-32 bg-primary/20 rounded-full blur-2xl" />
                    )}
                    
                    {/* Category */}
                    <p className="text-muted-foreground text-sm font-medium">{item.category}</p>
                    
                    {/* APY */}
                    <div>
                      <p className={`text-3xl font-bold ${item.isHighlight ? 'text-primary' : 'text-card-foreground'}`}>
                        {item.apy}
                        <span className="text-sm font-normal text-muted-foreground ml-1">APY</span>
                      </p>
                    </div>
                    
                    {/* Coins with icons */}
                    <div className="flex items-center gap-2">
                      {item.coins.map((coin) => (
                        <span key={coin} className="flex items-center gap-1.5 text-xs text-muted-foreground bg-secondary px-2 py-1 rounded">
                          <CoinIcon coin={coin} />
                          {coin}
                        </span>
                      ))}
                    </div>
                  </motion.div>
                ))}
              </div>
            </div>
          </div>
          
          {/* Shared Description */}
          <div className="mt-6 bg-card border border-border rounded-xl p-4 text-center">
            <p className="text-muted-foreground text-sm">
              {isZh ? "固定期限提供更高的可预测性，并附带更高的利率收益" : "Fixed terms provide higher predictability with higher interest rates"}
            </p>
          </div>
        </section>

        {/* Process Section with Glass Cards */}
        <section className="max-w-6xl mx-auto px-4 py-20">
          <h3 className="text-2xl font-bold text-card-foreground mb-12 text-center">
            {isZh ? "操作流程" : "How It Works"}
          </h3>
          
          <div className="grid md:grid-cols-3 gap-6">
            {steps.map((step, i) => (
              <div key={i} className="relative">
                <motion.div
                  whileHover={{ y: -8, scale: 1.02 }}
                  transition={{ duration: 0.3 }}
                  className="relative bg-card border border-border rounded-2xl p-8 hover:border-primary/50 hover:shadow-[0_0_40px_rgba(var(--primary)/0.15)] transition-all duration-300 text-center"
                >
                  {/* Number */}
                  <div className="text-5xl font-bold text-primary/20 mb-4">
                    {step.number}
                  </div>
                  
                  {/* Title */}
                  <h4 className="text-xl text-card-foreground font-bold mb-3">{step.title}</h4>
                  
                  {/* Description */}
                  <p className="text-muted-foreground text-sm leading-relaxed">{step.description}</p>
                </motion.div>
              </div>
            ))}
          </div>
        </section>

        {/* Benefits Section */}
        <section className="max-w-6xl mx-auto px-4 pb-20">
          <div className="grid lg:grid-cols-2 gap-10">
            {/* Left - Benefits List */}
            <div>
              <h3 className="text-2xl font-bold text-card-foreground mb-8">
                {isZh ? "为什么选择 Monera Digital？" : "Why Choose Monera Digital?"}
              </h3>
              
              <div className="space-y-4">
                {benefits.map((item, i) => (
                  <motion.div
                    key={i}
                    whileHover={{ x: 8, scale: 1.02 }}
                    transition={{ duration: 0.3 }}
                    className="flex items-start gap-5 p-5 bg-card border border-border rounded-2xl hover:border-primary/50 hover:shadow-[0_0_20px_rgba(var(--primary)/0.1)] transition-all duration-300 cursor-pointer group"
                  >
                    <div className="w-12 h-12 rounded-xl bg-primary/10 flex items-center justify-center text-primary flex-shrink-0 group-hover:bg-primary/20 transition-colors">
                      {item.icon}
                    </div>
                    <div>
                      <h4 className="text-card-foreground font-semibold mb-1">{item.title}</h4>
                      <p className="text-muted-foreground text-sm">{item.description}</p>
                    </div>
                  </motion.div>
                ))}
              </div>
            </div>

            {/* Right - Highlight Card with Gradient Border */}
            <div className="flex flex-col">
              <div className="text-2xl font-bold text-card-foreground mb-8 lg:opacity-0">
                {isZh ? "为什么选择 Monera Digital？" : "Why Choose Monera Digital?"}
              </div>
              <div className="relative flex-1 min-h-[280px]">
                {/* Gradient Border Effect */}
                <div className="absolute -inset-[1px] bg-gradient-to-br from-primary/50 via-primary/20 to-transparent rounded-3xl opacity-50" />
                <div className="absolute -inset-[1px] bg-background rounded-3xl" />
                
                <motion.div
                  whileHover={{ scale: 1.02 }}
                  transition={{ duration: 0.3 }}
                  className="relative bg-background rounded-3xl p-8 border border-primary/30 shadow-[0_0_40px_rgba(var(--primary)/0.3)] h-full flex flex-col justify-between"
                >
                {/* Glow Effects */}
                <div className="absolute -top-20 -right-20 w-48 h-48 bg-primary/20 rounded-full blur-3xl" />
                <div className="absolute -bottom-20 -left-20 w-32 h-32 bg-primary/10 rounded-full blur-2xl" />
                
                <div className="relative z-10 space-y-8">
                  {/* Yield */}
                  <div>
                    <p className="text-muted-foreground text-sm mb-3 uppercase tracking-wider">
                      {isZh ? "参考收益率" : "Reference Yield"}
                    </p>
                    <div className="flex items-baseline gap-2">
                      <span className="text-6xl lg:text-7xl font-extrabold text-transparent bg-clip-text bg-gradient-to-r from-primary to-primary/80">
                        8%
                      </span>
                    </div>
                  </div>

                  <div className="h-px bg-gradient-to-r from-primary/50 via-primary/20 to-transparent" />

                  {/* Minimum Amount */}
                  <div>
                    <p className="text-muted-foreground text-sm mb-3 uppercase tracking-wider">
                      {isZh ? "最低借出金额" : "Minimum Amount"}
                    </p>
                    <p className="text-3xl lg:text-4xl font-bold text-card-foreground">
                      50,000 <span className="text-muted-foreground text-lg">{isZh ? "美元" : "USD"}</span>
                    </p>
                  </div>
                </div>
              </motion.div>
              </div>
            </div>
          </div>
        </section>

        {/* CTA Section */}
        <section className="max-w-6xl mx-auto px-4 pb-20">
          <div className="relative bg-white/5 backdrop-blur-xl border border-white/10 rounded-3xl p-10 lg:p-16 text-center overflow-hidden">
            {/* Background Glow */}
            <div className="absolute top-0 left-1/2 -translate-x-1/2 w-96 h-32 bg-teal-400/10 rounded-full blur-3xl" />
            
            <div className="relative z-10">
              <h2 className="text-3xl lg:text-4xl font-bold text-white mb-4">
                {isZh ? "准备好开始赚取收益了吗？" : "Ready to Start Earning?"}
              </h2>
              <p className="text-gray-400 mb-10 max-w-xl mx-auto">
                {user 
                  ? (isZh ? "开始您的数字资产投资之旅。" : "Start your digital asset investment journey.")
                  : (isZh ? "立即注册 Monera Digital，体验机构级的数字资产借贷服务。" : "Register now at Monera Digital and experience institutional-grade digital asset lending services.")
                }
              </p>
              
              <div className="flex flex-col sm:flex-row gap-4 justify-center">
                {user ? (
                  <Button 
                    className="bg-gradient-to-r from-teal-500 to-teal-400 hover:from-teal-400 hover:to-teal-300 text-black font-semibold px-10 py-4 rounded-xl shadow-[0_0_30px_rgba(46,230,214,0.4)] transition-all duration-300"
                    onClick={() => navigate("/dashboard")}
                  >
                    {isZh ? "进入管理面板" : "Go to Dashboard"}
                    <ChevronRight className="ml-2 w-5 h-5" />
                  </Button>
                ) : (
                  <>
                    <Button 
                      className="bg-gradient-to-r from-teal-500 to-teal-400 hover:from-teal-400 hover:to-teal-300 text-black font-semibold px-10 py-4 rounded-xl shadow-[0_0_30px_rgba(46,230,214,0.4)] transition-all duration-300"
                      onClick={() => navigate("/register")}
                    >
                      {isZh ? "立即注册" : "Register Now"}
                      <ChevronRight className="ml-2 w-5 h-5" />
                    </Button>
                    <Button 
                      variant="outline"
                      className="border-teal-400/40 text-teal-400 hover:bg-teal-400/10 backdrop-blur-sm px-10 py-4 rounded-xl transition-all duration-300"
                      onClick={() => navigate("/login")}
                    >
                      {isZh ? "登录账户" : "Login"}
                    </Button>
                  </>
                )}
              </div>
            </div>
          </div>
        </section>

        {/* Disclaimer */}
        <div className="max-w-6xl mx-auto px-4 pb-16">
          <div className="border-t border-white/5 pt-8">
            <h4 className="text-gray-400 text-sm font-semibold mb-4">{isZh ? "免责声明" : "Disclaimer"}</h4>
            <p className="text-gray-500 text-xs leading-relaxed max-w-4xl whitespace-pre-line">
              {isZh 
                ? `本材料中关于出借产品（"产品"）的信息由 Monera Pte. Ltd.（"Monera Digital"）提供，仅供参考，不构成购买或出售金融产品的要约或招揽。投资数字资产涉及重大风险，包括可能损失全部投资资金。此外，由于与数字资产交易所的间接关联，投资者可能会因这些交易所的任何信用或安全事件而受到不利影响。Monera Digital 不对该产品的盈利能力或其是否适合个人的财务目标或风险承受能力作出任何保证。

本材料中包含的信息可能会有所变更，Monera Digital 不对依赖或使用本材料所造成的任何损失或损害承担责任。Monera Digital 不受新加坡金融管理局（MAS）监管。本产品可能被视为一种加密货币衍生品，但不受 MAS 监管。投资者在授资本产品时将无法享受立法保护，且本产品不受通常适用于受监管金融产品的要求约束。如有疑问，您应咨询独立的财务顾问。本材料中的信息均未经 MAS 审查或批准。定价信息截至 2026 年 6 月。`
                : `The information about the lending product ("Product") in this material is provided by Monera Pte. Ltd. ("Monera Digital") for reference only and does not constitute an offer or solicitation to purchase or sell financial products. Investing in digital assets involves significant risks, including the possible loss of the entire investment funds. Additionally, due to indirect association with digital asset exchanges, investors may be adversely affected by any credit or security incidents of these exchanges. Monera Digital makes no guarantee regarding the product's profitability or its suitability for an individual's financial goals or risk tolerance.

The information contained in this material may be subject to change. Monera Digital shall not be liable for any loss or damage arising from reliance on or use of this material. Monera Digital is not regulated by the Monetary Authority of Singapore (MAS). This product may be considered a cryptocurrency derivative but is not regulated by MAS. Investors will not enjoy legislative protection when investing in this product, and this product is not subject to requirements that typically apply to regulated financial products. If in doubt, you should consult an independent financial adviser. The information in this material has not been reviewed or approved by MAS. Pricing information is as of June 2026.`
              }
            </p>
          </div>
        </div>
      </div>

      <Footer />
    </div>
  );
};

export default StructuredProducts;
