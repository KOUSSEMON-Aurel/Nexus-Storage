import { useState, useEffect } from "react";
import { motion, AnimatePresence } from "framer-motion";
import { 
  Cloud, 
  HardDrive, 
  Settings, 
  Shield, 
  Upload, 
  Download, 
  File, 
  Folder, 
  MoreVertical,
  Search,
  Plus,
  Zap,
  LayoutDashboard,
  Clock,
  Terminal
} from "lucide-react";
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export default function App() {
  const [activeTab, setActiveTab] = useState("dashboard");
  const [isUploading, setIsUploading] = useState(false);

  return (
    <div className="flex h-screen w-full bg-[#09090b] text-foreground font-sans selection:bg-primary/30 gradient-bg">
      {/* Sidebar */}
      <aside className="w-64 border-r border-border flex flex-col glass z-10">
        <div className="p-6 flex items-center gap-3">
          <div className="w-8 h-8 bg-primary rounded-lg flex items-center justify-center shadow-lg shadow-primary/20">
            <Zap className="w-5 h-5 text-white" />
          </div>
          <h1 className="text-xl font-bold tracking-tight">Nexus</h1>
        </div>

        <nav className="flex-1 px-4 py-4 space-y-1">
          <NavItem 
            icon={<LayoutDashboard size={20} />} 
            label="Dashboard" 
            active={activeTab === "dashboard"} 
            onClick={() => setActiveTab("dashboard")} 
          />
          <NavItem 
            icon={<HardDrive size={20} />} 
            label="My Storage" 
            active={activeTab === "storage"} 
            onClick={() => setActiveTab("storage")} 
          />
          <NavItem 
            icon={<Clock size={20} />} 
            label="Recent" 
            active={activeTab === "recent"} 
            onClick={() => setActiveTab("recent")} 
          />
          <NavItem 
            icon={<Terminal size={20} />} 
            label="Terminal" 
            active={activeTab === "terminal"} 
            onClick={() => setActiveTab("terminal")} 
          />
          <div className="pt-4 pb-2 px-2 text-xs font-semibold text-muted-foreground uppercase tracking-wider">
            System
          </div>
          <NavItem 
            icon={<Shield size={20} />} 
            label="Security" 
            active={activeTab === "security"} 
            onClick={() => setActiveTab("security")} 
          />
          <NavItem 
            icon={<Settings size={20} />} 
            label="Settings" 
            active={activeTab === "settings"} 
            onClick={() => setActiveTab("settings")} 
          />
        </nav>

        <div className="p-4 mt-auto">
          <div className="glass-card p-4 rounded-xl space-y-3">
            <div className="flex justify-between items-center text-xs">
              <span className="text-muted-foreground">Unlimited Pool</span>
              <span className="text-primary font-medium">Active</span>
            </div>
            <div className="h-1.5 w-full bg-muted rounded-full overflow-hidden">
              <motion.div 
                initial={{ width: 0 }}
                animate={{ width: "65%" }}
                className="h-full bg-primary"
              />
            </div>
            <p className="text-[10px] text-muted-foreground leading-tight">
              Using YouTube infra for storage. No central limits apply.
            </p>
          </div>
        </div>
      </aside>

      {/* Main Content */}
      <main className="flex-1 flex flex-col relative overflow-hidden">
        {/* Header */}
        <header className="h-16 border-b border-border flex items-center justify-between px-8 glass-card z-10">
          <div className="flex items-center glass-card px-3 py-1.5 rounded-lg border-border w-96">
            <Search size={18} className="text-muted-foreground mr-2" />
            <input 
              type="text" 
              placeholder="Search files and videos..." 
              className="bg-transparent border-none outline-none text-sm w-full"
            />
          </div>

          <div className="flex items-center gap-4">
            <button 
              onClick={() => setIsUploading(true)}
              className="flex items-center gap-2 bg-primary hover:bg-primary/90 text-white px-4 py-2 rounded-lg text-sm font-medium transition-all shadow-lg shadow-primary/20 active:scale-95"
            >
              <Plus size={18} />
              Upload file
            </button>
            <div className="w-8 h-8 rounded-full bg-muted border border-border flex items-center justify-center overflow-hidden">
              <img src="https://api.dicebear.com/7.x/avataaars/svg?seed=Aurel" alt="User" />
            </div>
          </div>
        </header>

        {/* Scrollable Area */}
        <div className="flex-1 overflow-y-auto p-8 custom-scrollbar">
          <AnimatePresence mode="wait">
            <motion.div
              key={activeTab}
              initial={{ opacity: 0, y: 10 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -10 }}
              transition={{ duration: 0.2 }}
            >
              <h2 className="text-2xl font-bold mb-6 flex items-center gap-3">
                {activeTab === 'dashboard' && 'Welcome back, Aurel'}
                {activeTab === 'storage' && 'My YouTube Storage'}
                {activeTab === 'security' && 'Security Protocol'}
              </h2>

              {activeTab === 'dashboard' && <DashboardView />}
              {activeTab === 'storage' && <StorageView />}
            </motion.div>
          </AnimatePresence>
        </div>
      </main>

      {/* Overlay Upload Panel */}
      <AnimatePresence>
        {isUploading && (
          <>
            <motion.div 
              initial={{ opacity: 0 }}
              animate={{ opacity: 1 }}
              exit={{ opacity: 0 }}
              onClick={() => setIsUploading(false)}
              className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50"
            />
            <motion.div 
              initial={{ scale: 0.9, opacity: 0, y: 20 }}
              animate={{ scale: 1, opacity: 1, y: 0 }}
              exit={{ scale: 0.9, opacity: 0, y: 20 }}
              className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[500px] glass p-8 z-[60] rounded-2xl"
            >
              <h3 className="text-xl font-bold mb-4">Upload to YouTube</h3>
              <div className="border-2 border-dashed border-muted rounded-xl p-12 flex flex-col items-center justify-center gap-4 hover:border-primary/50 transition-colors group cursor-pointer">
                <div className="w-16 h-16 rounded-full bg-primary/10 flex items-center justify-center group-hover:scale-110 transition-transform">
                  <Upload className="text-primary" size={32} />
                </div>
                <div className="text-center">
                  <p className="font-medium">Drop files here or click to browse</p>
                  <p className="text-sm text-muted-foreground mt-1">Files will be encrypted and encoded as pixels</p>
                </div>
              </div>
              <div className="mt-8 flex justify-end gap-3">
                <button onClick={() => setIsUploading(false)} className="px-4 py-2 text-sm font-medium hover:text-primary transition-colors">Cancel</button>
                <button className="bg-primary text-white px-6 py-2 rounded-lg text-sm font-medium shadow-lg shadow-primary/20">Start Archive</button>
              </div>
            </motion.div>
          </>
        )}
      </AnimatePresence>
    </div>
  );
}

function NavItem({ icon, label, active, onClick }: { icon: any, label: string, active?: boolean, onClick: () => void }) {
  return (
    <button 
      onClick={onClick}
      className={cn(
        "w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm font-medium transition-all duration-200 group",
        active 
          ? "bg-primary/10 text-primary shadow-[inset_0_1px_0_0_rgba(255,255,255,0.05)]" 
          : "text-muted-foreground hover:bg-muted hover:text-foreground"
      )}
    >
      <span className={cn(
        "transition-transform duration-200 group-hover:scale-110",
        active ? "text-primary" : "text-muted-foreground group-hover:text-foreground"
      )}>
        {icon}
      </span>
      {label}
      {active && <motion.div layoutId="active-pill" className="ml-auto w-1 h-4 bg-primary rounded-full" />}
    </button>
  );
}

function DashboardView() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
      <StatCard 
        title="Stored Data" 
        value="1.2 TB" 
        sub="Unlimited pool active" 
        icon={<Cloud className="text-blue-400" />} 
        trend="+12 GB today"
      />
      <StatCard 
        title="Security Score" 
        value="A+" 
        sub="End-to-End Encrypted" 
        icon={<Shield className="text-green-400" />} 
        trend="Perfect health"
      />
      <StatCard 
        title="Upload Queue" 
        value="4 Files" 
        sub="Pending processing" 
        icon={<Upload className="text-orange-400" />} 
        trend="Auto-transcoding"
      />

      <div className="md:col-span-2 glass-card rounded-2xl p-6">
        <h3 className="text-lg font-bold mb-4">Recent Activity</h3>
        <div className="space-y-4">
          <ActivityItem 
            type="file" 
            name="nexus_v1_backup.rar" 
            status="Uploaded to YT: h6Xw9Gk2..." 
            time="2 mins ago" 
            size="450 MB"
          />
          <ActivityItem 
            type="folder" 
            name="Photos_Work_2024" 
            status="Synced with drive" 
            time="1 hour ago" 
            size="12 GB"
          />
          <ActivityItem 
            type="file" 
            name="private_keys.enc" 
            status="Decrypted locally" 
            time="3 hours ago" 
            size="2 KB"
          />
        </div>
      </div>

      <div className="glass-card rounded-2xl p-6">
        <h3 className="text-lg font-bold mb-4">Quick Links</h3>
        <div className="space-y-2">
          <QuickLink label="Generate Magic Link" icon={<Download size={16} />} />
          <QuickLink label="Mount Virtual Drive" icon={<HardDrive size={16} />} />
          <QuickLink label="View Tube Metadata" icon={<Search size={16} />} />
          <QuickLink label="API Quota Prediction" icon={<Zap size={16} />} />
        </div>
      </div>
    </div>
  );
}

function StorageView() {
  const files = [
    { name: "Project_Aurora_Source.zip", size: "1.2 GB", id: "xP91kL2", date: "Mar 24, 2024" },
    { name: "Family_Holidays_4K.mp4", size: "8.5 GB", id: "mJ00zW9", date: "Mar 22, 2024" },
    { name: "Secrets_Vault.nexus", size: "12 KB", id: "kK88vR4", date: "Mar 21, 2024" },
    { name: "Work_Documents_PDF.7z", size: "240 MB", id: "tU44nH7", date: "Mar 19, 2024" },
  ];

  return (
    <div className="glass-card rounded-2x overflow-hidden">
      <table className="w-full text-left">
        <thead>
          <tr className="border-b border-border text-xs text-muted-foreground uppercase tracking-wider">
            <th className="px-6 py-4 font-semibold">File Name</th>
            <th className="px-6 py-4 font-semibold">Size</th>
            <th className="px-6 py-4 font-semibold">YouTube ID</th>
            <th className="px-6 py-4 font-semibold">Created</th>
            <th className="px-6 py-4 font-semibold"></th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {files.map((file, i) => (
            <tr key={i} className="hover:bg-muted/50 transition-colors group cursor-pointer">
              <td className="px-6 py-4">
                <div className="flex items-center gap-3">
                  <div className="p-2 rounded-lg bg-primary/10 text-primary">
                    <File size={18} />
                  </div>
                  <span className="font-medium">{file.name}</span>
                </div>
              </td>
              <td className="px-6 py-4 text-sm text-muted-foreground">{file.size}</td>
              <td className="px-6 py-4">
                <code className="text-[10px] bg-muted px-2 py-1 rounded text-primary">{file.id}</code>
              </td>
              <td className="px-6 py-4 text-sm text-muted-foreground">{file.date}</td>
              <td className="px-6 py-4 text-right">
                <button className="opacity-0 group-hover:opacity-100 transition-opacity p-1.5 hover:bg-muted rounded text-muted-foreground">
                  <MoreVertical size={18} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function StatCard({ title, value, sub, icon, trend }: any) {
  return (
    <div className="glass p-6 rounded-2xl space-y-4">
      <div className="flex justify-between items-start">
        <div className="p-2 rounded-xl bg-background border border-border">
          {icon}
        </div>
        <span className="text-[10px] font-bold text-green-400 bg-green-400/10 px-2 py-0.5 rounded-full uppercase tracking-wider">
          {trend}
        </span>
      </div>
      <div>
        <p className="text-sm text-muted-foreground">{title}</p>
        <p className="text-3xl font-bold tracking-tight">{value}</p>
        <p className="text-xs text-muted-foreground mt-1">{sub}</p>
      </div>
    </div>
  );
}

function ActivityItem({ type, name, status, time, size }: any) {
  return (
    <div className="flex items-center gap-4 group">
      <div className={cn(
        "w-10 h-10 rounded-xl flex items-center justify-center border border-border",
        type === 'file' ? "bg-blue-400/10 text-blue-400" : "bg-orange-400/10 text-orange-400"
      )}>
        {type === 'file' ? <File size={20} /> : <Folder size={20} />}
      </div>
      <div className="flex-1">
        <p className="text-sm font-medium leading-none">{name}</p>
        <p className="text-xs text-muted-foreground mt-1">{status}</p>
      </div>
      <div className="text-right">
        <p className="text-xs font-medium">{size}</p>
        <p className="text-[10px] text-muted-foreground">{time}</p>
      </div>
    </div>
  );
}

function QuickLink({ label, icon }: any) {
  return (
    <button className="w-full flex items-center gap-3 p-3 rounded-xl hover:bg-muted transition-colors text-sm font-medium group">
      <div className="text-muted-foreground group-hover:text-primary transition-colors">
        {icon}
      </div>
      {label}
    </button>
  );
}
