import Header from "@/components/Header";
import Hero from "@/components/Hero";
import Stats from "@/components/Stats";
import FundPerformance from "@/components/FundPerformance";
import Features from "@/components/Features";
import HowItWorks from "@/components/HowItWorks";
import CTA from "@/components/CTA";
import About from "@/components/About";
import Insights from "@/components/Insights";
import Footer from "@/components/Footer";

const Index = () => {
  return (
    <div className="min-h-screen bg-background">
      <Header />
      <main>
        <Hero />
        <Stats />
        <FundPerformance />
        <Features />
        <HowItWorks />
        <About />
        <Insights />
        <CTA />
      </main>
      <Footer />
    </div>
  );
};

export default Index;
