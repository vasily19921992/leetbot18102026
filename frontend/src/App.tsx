import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { ArrowDownRight, ArrowLeft, ArrowRight, ArrowUpRight, Award, BookOpen, Check, ChevronDown, CircleHelp, Clock3, Flame, Grid2X2, LockKeyhole, LogIn, LogOut, Menu, Play, Sparkles, Target, X, Zap } from "lucide-react";

type Page = "home" | "train" | "schulte" | "progress";
type User = { email: string; accessUntil: string; accessActive: boolean; plan: "trial" | "paid"; completed: number[]; focusSeconds: number };
type Exercise = { id: number; title: string; category: string; level: number; levelName: string; text: string; question: string; options: string[]; correctOption: number };
type AuthMode = "login" | "register";
type Payment = Record<string, string>;

async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, { credentials: "include", ...options, headers: { "Content-Type": "application/json", ...options.headers } });
  const payload = response.status === 204 ? null : await response.json();
  if (!response.ok) throw new Error(payload?.error || "Что-то пошло не так. Попробуйте ещё раз.");
  return payload as T;
}
const navItems: { id: Page; label: string }[] = [
  { id: "home", label: "Главная" }, { id: "train", label: "Тренировка" },
  { id: "schulte", label: "Шульте" }, { id: "progress", label: "Прогресс" },
];
const leet: Record<string, string> = { а:"4", б:"6", в:"8", г:"r", д:"g", е:"3", з:"3", и:"u", к:"k", м:"m", н:"h", о:"0", р:"p", с:"c", т:"7", у:"y", ф:"ф", х:"x", ц:"ц", ч:"4", ш:"ш", щ:"щ", ы:"ы", э:"э", я:"я" };
function toLeet(char: string) {
  const lower = char.toLowerCase(), replacement = leet[lower];
  return replacement ? (char === lower ? replacement : replacement.toUpperCase()) : char;
}
function localDone(): number[] {
  try { return JSON.parse(localStorage.getItem("letri.completed") || "[]"); } catch { return []; }
}
function dayKey(date = new Date()) {
  return [date.getFullYear(), String(date.getMonth()+1).padStart(2,"0"), String(date.getDate()).padStart(2,"0")].join("-");
}
function streakCount() {
  try {
    const days = new Set<string>(JSON.parse(localStorage.getItem("letri.days") || "[]"));
    const today = new Date(); let total = 0;
    for (let n=0; n<365; n++) {
      if (!days.has(dayKey(new Date(today.getFullYear(), today.getMonth(), today.getDate()-n)))) break;
      total++;
    }
    return total;
  } catch { return 0; }
}

export default function App() {
  const [page, setPage] = useState<Page>("home");
  const [user, setUser] = useState<User|null>(null);
  const [exercises, setExercises] = useState<Exercise[]>([]);
  const [backendError, setBackendError] = useState("");
  const [authOpen, setAuthOpen] = useState(false);
  const [authMode, setAuthMode] = useState<AuthMode>("register");
  const [exerciseId, setExerciseId] = useState<number|null>(null);
  const [guestDone, setGuestDone] = useState(localDone);
  const [payment, setPayment] = useState<Payment|null>(null);
  const [toast, setToast] = useState("");
  const [mobileMenu, setMobileMenu] = useState(false);
  const [best, setBest] = useState<number|null>(() => { const n=Number(localStorage.getItem("letri.schulteBest")); return n>0?n:null; });

  useEffect(() => {
    if (new URLSearchParams(location.search).get("payment") !== "waiting") return;
    let attempts = 0;
    const timer = window.setInterval(() => {
      attempts++;
      api<{user:User}>("/api/me").then((data) => {
        setUser(data.user);
        if (data.user.plan === "paid") {
          setToast("Оплата подтверждена. Доступ открыт на 30 дней.");
          window.clearInterval(timer);
        }
      }).catch(() => undefined);
      if (attempts >= 24) window.clearInterval(timer);
    }, 5000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    const openSchulte = () => {
      setPage("schulte");
      setExerciseId(null);
      window.scrollTo({top:0,behavior:"smooth"});
    };
    window.addEventListener("open-schulte", openSchulte);
    return () => window.removeEventListener("open-schulte", openSchulte);
  }, []);
  useEffect(() => {
    api<Exercise[]>("/api/exercises").then(setExercises).catch(() => setBackendError("Сервер пока не запущен. Запусти backend по инструкции в README, чтобы открыть упражнения и аккаунт."));
    api<{user:User}>("/api/me").then((data) => setUser(data.user)).catch(() => undefined);
    if (new URLSearchParams(location.search).get("payment")==="waiting") setToast("Платёж проверяется ЮMoney. Доступ включится после подтверждения перевода.");
  }, []);
  useEffect(() => {
    if (!toast) return;
    const timer=window.setTimeout(() => setToast(""),5000);
    return () => window.clearTimeout(timer);
  },[toast]);
  useEffect(() => { if(payment) (document.getElementById("yoomoney-payment-form") as HTMLFormElement | null)?.requestSubmit(); },[payment]);

  const completed = user?.completed ?? guestDone;
  const activeExercise = exercises.find((item) => item.id===exerciseId) ?? null;
  const locked = !user || !user.accessActive;

  function navigate(next:Page) {
    setPage(next); setExerciseId(null); setMobileMenu(false); window.scrollTo({top:0,behavior:"smooth"});
  }
  function openAuth(mode:AuthMode="register") { setAuthMode(mode); setAuthOpen(true); }
  function onAuthenticated(next:User) { setUser(next); setAuthOpen(false); setToast("Добро пожаловать! Бесплатный доступ открыт на 1 день."); }
  function startExercise(id?:number) {
    if(locked) {
      setPage("progress");
      setToast(user ? "Продли доступ, чтобы продолжить." : "Создай аккаунт, чтобы сохранить прогресс и открыть остальные задания.");
      return;
    }
    setPage("train");
    setExerciseId(id ?? (exercises.find((item)=>!completed.includes(item.id))?.id || 1));
    window.scrollTo({top:0,behavior:"smooth"});
  }
  async function beginPayment() {
    if(!user) { openAuth(); return; }
    try { setPayment(await api<Payment>("/api/payment/start",{method:"POST",body:"{}"})); }
    catch(error) { setToast(error instanceof Error ? error.message : "Не удалось подготовить оплату."); }
  }
  function logout() {
    api("/api/logout",{method:"POST",body:"{}"}).finally(()=>{setUser(null);setPage("home");setToast("Ты вышел из аккаунта.");});
  }
  function recordDay() {
    const days:string[]=JSON.parse(localStorage.getItem("letri.days")||"[]");
    localStorage.setItem("letri.days",JSON.stringify([...new Set([...days,dayKey()])]));
  }
  function completeLocal(id:number) {
    const next=guestDone.includes(id)?guestDone:[...guestDone,id];
    setGuestDone(next);localStorage.setItem("letri.completed",JSON.stringify(next));recordDay();
  }
  async function completeExercise(id:number,seconds:number) {
    if(!user) { completeLocal(id); return; }
    try {
      const data=await api<{user:User}>("/api/progress",{method:"POST",body:JSON.stringify({exerciseId:id,focusDelta:seconds})});
      setUser(data.user);recordDay();
    } catch(error) { setToast(error instanceof Error?error.message:"Прогресс не сохранился."); }
  }
  function completeSchulte(seconds:number) {
    if(!best||seconds<best) { setBest(seconds);localStorage.setItem("letri.schulteBest",String(seconds)); }
    recordDay();
  }

  return <div className="app-shell">
    {payment&&<form id="yoomoney-payment-form" className="hidden-form" action={payment.action} method="POST">
      {Object.entries(payment).filter(([key])=>key!=="action").map(([key,value])=><input key={key} type="hidden" name={key} value={value}/>)}
      <button type="submit">Перейти к оплате</button>
    </form>}
    <header className="site-header">
      <a className="brand" href="#" onClick={(e)=>{e.preventDefault();navigate("home");}}>
        <span className="brand-mark"><span>ле</span><b>т</b></span><span className="brand-copy"><strong>ЛЕТРИ</strong><small>МЫСЛИ В ТОНУСЕ</small></span>
      </a>
      <nav className={"main-nav "+(mobileMenu?"main-nav-open":"")} aria-label="Основное меню">
        {navItems.map((item)=><button key={item.id} className={"nav-link "+(page===item.id?"nav-active":"")} onClick={()=>navigate(item.id)}>{item.label}</button>)}
      </nav>
      <div className="header-actions">
        {user?<button className="account-pill" title={user.email} onClick={()=>navigate("progress")}><span className="account-dot"/>{user.email.split("@")[0]}</button>
          :<button className="login-link" onClick={()=>openAuth("login")}><LogIn size={16}/>Войти</button>}
        <button className="header-cta" onClick={()=>user?startExercise():openAuth()}>{user?"Продолжить":"Начать бесплатно"}<ArrowUpRight size={15}/></button>
      </div>
      <button className="mobile-menu-button" aria-label="Меню" onClick={()=>setMobileMenu(!mobileMenu)}>{mobileMenu?<X/>:<Menu/>}</button>
    </header>
    {backendError&&<div className="backend-banner"><CircleHelp size={17}/><span>{backendError}</span></div>}
    {toast&&<div className="toast"><Sparkles size={16}/>{toast}<button aria-label="Закрыть" onClick={()=>setToast("")}><X size={15}/></button></div>}
    <main>
      {page==="home"&&<HomePage completed={completed.length} streak={streakCount()} onStart={()=>startExercise()} onAuth={()=>openAuth()} onNavigate={navigate}/>}
      {page==="train"&&<TrainingPage exercises={exercises} completed={completed} active={activeExercise} locked={locked} guest={!user}
        onSelect={startExercise} onComplete={completeExercise}
        onNext={()=>startExercise(activeExercise?.id&&activeExercise.id<exercises.length?activeExercise.id+1:1)} onPay={beginPayment}/>}
      {page==="schulte"&&locked&&<AccessGate guest={!user} onAction={user?beginPayment:()=>openAuth()}/>}
      {page==="schulte"&&!locked&&<SchultePage best={best} onComplete={completeSchulte}/>}
      {page==="progress"&&<ProgressPage user={user} completed={completed} streak={streakCount()} best={best} onPay={beginPayment} onAuth={()=>openAuth()} onLogout={logout} onStart={()=>startExercise()}/>}
    </main>
    <footer className="site-footer"><div className="footer-brand"><span className="brand-mark"><span>ле</span><b>т</b></span><span>Маленькая тренировка. Большая привычка.</span></div><span>© 2026 Летри · 10 минут внимания в день</span><button onClick={()=>setToast("Практика помогает тренировать внимание и наблюдательность. Она не заменяет отдых и заботу о здоровье.")}>О проекте <ArrowUpRight size={14}/></button></footer>
    {authOpen&&<AuthModal mode={authMode} onMode={setAuthMode} onClose={()=>setAuthOpen(false)} onAuthenticated={onAuthenticated}/>}
  </div>;
}
function HomePage(props:{completed:number;streak:number;onStart:()=>void;onAuth:()=>void;onNavigate:(page:Page)=>void}) {
  return <>
    <section className="hero section-wrap">
      <div className="hero-copy">
        <div className="eyebrow"><i/>ЕЖЕДНЕВНАЯ ПРАКТИКА · 10 МИНУТ</div>
        <h1>Читай<br/><em>между</em> строк.</h1>
        <p className="hero-lead">Небольшая тренировка внимания: разгадывай кириллические тексты, ищи числа в таблицах Шульте и замечай, как растёт твой результат.</p>
        <div className="hero-buttons"><button className="button button-dark" onClick={props.onAuth}>Начать бесплатно <ArrowRight size={17}/></button><button className="text-button" onClick={()=>props.onNavigate("train")}><Play size={16}/> Посмотреть задания</button></div>
        <div className="hero-proof"><div className="avatar-stack"><span>Л</span><span>М</span><span>О</span><span>+</span></div><div><strong>Коротко и по делу</strong><small>Пять минут — уже практика</small></div><i className="proof-separator"/><b className="proof-star">✳</b><div><strong>520 текстов</strong><small>Сложность растёт постепенно</small></div></div>
      </div>
      <div className="hero-art">
        <div className="art-sun"/><div className="art-topline"><span>ТЕКСТ № 024</span><span>01 / 04</span></div>
        <div className="art-note"><span className="note-label">ЗАМЕТКА В ПОЛЯХ</span><p>м3ждy д3л<br/>u в4жнbх д4т<br/><b>п0м3cтu «b3т3p<br/>п3p3лuc7ыв43т<br/>0бл4к4»</b></p><span className="note-scribble">прочитай<br/>не спеша ↗</span></div>
        <div className="art-bottom"><span className="art-check"><Check size={15}/></span><span>внимание +1</span><span className="art-chip">ПОПРОБУЙ</span></div>
        <div className="art-orbit orbit-one"/><div className="art-orbit orbit-two"/><span className="art-spark spark-a">✳</span><span className="art-spark spark-b">✴</span><span className="art-tag">не торопись</span>
      </div>
    </section>
    <section className="metrics-row section-wrap">
      <div className="metric"><strong>520</strong><span>коротких текстов</span></div><div className="metric-divider"/>
      <div className="metric"><strong>4 <small>уровня</small></strong><span>от статики до движения</span></div><div className="metric-divider"/>
      <div className="metric"><strong>1 <small>день</small></strong><span>на бесплатное знакомство</span></div>
      <div className="metric-cta"><span>Ритм важнее рекорда</span><ArrowDownRight size={18}/></div>
    </section>
    <section className="practice-section section-wrap">
      <div className="section-heading"><div><span className="section-kicker">ВЫБЕРИ СВОЮ ПРАКТИКУ</span><h2>Три способа<br/>вернуться в фокус.</h2></div><p>Привычка складывается из маленьких подходов. Выбери задачу по настроению и двигайся в своём темпе.</p></div>
      <div className="practice-grid">
        <button className="practice-card card-words" onClick={()=>props.onNavigate("train")}><span className="practice-icon"><BookOpen size={20}/></span><span className="practice-count">520 ЗАДАНИЙ</span><h3>Чтение<br/>между строк</h3><p>Стихи и мини-истории в кириллическом leet-шифре.</p><span className="card-arrow"><ArrowUpRight size={19}/></span><div className="card-decoration deco-letters">л3тpи</div></button>
        <button className="practice-card card-grid" onClick={()=>props.onNavigate("schulte")}><span className="practice-icon"><Grid2X2 size={20}/></span><span className="practice-count">ТАБЛИЦА 5 × 5</span><h3>Таблицы<br/>Шульте</h3><p>Ищи числа по порядку, сохраняя спокойный темп.</p><span className="card-arrow"><ArrowUpRight size={19}/></span><div className="mini-schulte">{[4,1,8,2,6,3,9,5,7].map((n,i)=><i key={i}>{n}</i>)}</div></button>
        <button className="practice-card card-progress" onClick={()=>props.onNavigate("progress")}><span className="practice-icon"><Target size={20}/></span><span className="practice-count">ЛИЧНЫЙ РИТМ</span><h3>Твой путь<br/>и прогресс</h3><p>Смотри, сколько заданий позади, и береги серию дней.</p><span className="card-arrow"><ArrowUpRight size={19}/></span><div className="progress-art">{[26,42,36,66,82,100].map((n,i)=><i key={i} style={{height:n+"%"}}/>)}</div></button>
      </div>
    </section>
    <section className="how-section section-wrap">
      <div className="how-illustration"><div className="how-ring ring-a"/><div className="how-ring ring-b"/><span className="how-eye">◉</span><span className="how-word">читай<br/><em>медленнее</em></span><span className="how-caption">и замечай больше</span><span className="how-star">✳</span></div>
      <div className="how-copy"><span className="section-kicker">НЕ ГОНКА. РИТМ.</span><h2>Мозгу полезны<br/>короткие паузы<br/>на внимание.</h2><p>Когда мы читаем внимательно, ищем закономерности или удерживаем взгляд на задаче, мы практикуем концентрацию. Летри превращает такую практику в спокойный ежедневный ритуал — без рекордов и бесконечных уведомлений.</p><div className="how-points"><div><span>01</span><p><b>Один текст за раз</b><small>Упражнения короткие, сложность растёт постепенно.</small></p></div><div><span>02</span><p><b>Твой темп — правильный</b><small>Никаких штрафов за паузы и пропущенные дни.</small></p></div></div><button className="text-button text-button-dark" onClick={props.onStart}>Попробовать упражнение <ArrowRight size={16}/></button></div>
    </section>
    <section className="quote-band section-wrap"><span className="quote-star">✳</span><p>Удели себе десять минут.<br/><em>В остальное время мир подождёт.</em></p><div className="quote-side"><span>ТВОЙ СЕГОДНЯШНИЙ РИТМ</span><strong>{props.streak?props.streak+" дн. подряд":"может начаться сегодня"}</strong><small>{props.completed?props.completed+" заданий уже позади":"первая практика — бесплатно"}</small></div></section>
    <section className="bottom-cta section-wrap"><div><span className="section-kicker">ГОТОВ НАЧАТЬ?</span><h2>Один шаг. Одна строка.<br/>И снова в фокусе.</h2></div><div className="bottom-cta-action"><button className="button button-yellow" onClick={props.onStart}>Открыть тренировку <ArrowRight size={17}/></button><span>Бесплатно на 1 день · 150 ₽ в месяц</span></div></section>
  </>;
}

function TrainingPage(props:{exercises:Exercise[];completed:number[];active:Exercise|null;locked:boolean;guest:boolean;onSelect:(id:number)=>void;onComplete:(id:number,seconds:number)=>Promise<void>;onNext:()=>void;onPay:()=>void}) {
  const [category,setCategory]=useState("Все"),[query,setQuery]=useState(""),[visibleCount,setVisibleCount]=useState(40);
  const [selected,setSelected]=useState<number|null>(null),[answer,setAnswer]=useState<"correct"|"wrong"|null>(null),[startedAt,setStartedAt]=useState(Date.now());
  const categories=["Все",...Array.from(new Set(props.exercises.map((item)=>item.category)))];
  const filtered=useMemo(()=>props.exercises.filter((item)=>(category==="Все"||item.category===category)&&item.title.toLowerCase().includes(query.toLowerCase())),[props.exercises,category,query]);
  useEffect(()=>{setSelected(null);setAnswer(null);setStartedAt(Date.now());},[props.active?.id]);
  function choose(index:number) {
    if(answer==="correct") return;
    setSelected(index);
    if(index===props.active?.correctOption&&props.active) {
      setAnswer("correct");
      void props.onComplete(props.active.id,Math.max(1,Math.round((Date.now()-startedAt)/1000)));
    } else setAnswer("wrong");
  }
  if(props.locked) return <AccessGate guest={props.guest} onAction={props.onPay}/>;
  return <section className="training-page section-wrap">
    <div className="page-topline"><div><span className="section-kicker">ЧИТАЙ И РАЗГАДЫВАЙ</span><h1>Тренировка</h1><p>Сначала текст неподвижен. Потом он начнёт двигаться — не торопись.</p></div><div className="training-stat"><Sparkles size={17}/><span><b>{props.completed.length}</b> / 520 пройдено</span></div></div>
    {props.active?<div className="exercise-workspace">
      <div className="exercise-main">
        <button className="back-link" onClick={()=>props.onSelect(0)}><ArrowLeft size={15}/> К списку заданий</button>
        <div className="exercise-heading"><span className="exercise-type">{props.active.category}</span><span className={"level-pill level-"+props.active.level}><i/>Уровень {props.active.level} · {props.active.levelName}</span></div>
        <h2>{props.active.title}</h2><p className="exercise-instruction">Прочитай текст и найди образ, который в нём спрятан.</p>
        <div className={"puzzle-card puzzle-level-"+props.active.level}><span className="puzzle-label">ТЕКСТ · {String(props.active.id).padStart(3,"0")}</span><div className="puzzle-text">
          {props.active.text.split("").map((char,index)=>{
            const active=props.active!.level>=2&&/[а-яё]/i.test(char);
            const special=props.active!.level===2?index%9===2:props.active!.level===3?index%11<3:index%7<3;
            const effect=active&&special?(props.active!.level===2?"glyph-flicker":props.active!.level===3?"glyph-spin":index%2?"glyph-flicker":"glyph-spin"):"";
            return <span key={index} className={"puzzle-glyph "+effect} style={{animationDelay:((index%13)*0.17)+"s"}}>{toLeet(char)}</span>;
          })}
        </div><div className="puzzle-card-foot"><span><Clock3 size={14}/> Без спешки</span><span><Zap size={14}/> +1 искра за правильный ответ</span></div></div>
        <div className="answer-panel"><div className="answer-title"><span className="answer-number">?</span><div><b>{props.active.question}</b><small>Выбери одну строку</small></div></div>
          <div className="answer-options">{props.active.options.map((option,index)=><button key={option} onClick={()=>choose(index)} disabled={answer==="correct"} className={"answer-option "+(selected===index?(index===props.active!.correctOption?"option-correct":"option-wrong"):"")+(answer==="correct"&&index===props.active!.correctOption?" option-correct":"")}><span>{String.fromCharCode(65+index)}</span>{option}{answer==="correct"&&index===props.active!.correctOption&&<Check size={16}/>}</button>)}</div>
          {answer==="wrong"&&<div className="answer-feedback feedback-wrong">Перечитай строку с кавычками — нужный образ спрятан там.</div>}
          {answer==="correct"&&<><div className="answer-feedback feedback-correct"><Sparkles size={16}/> Верно. Ты заметил главное.</div><button className="button button-dark next-exercise" onClick={props.onNext}>Следующее задание <ArrowRight size={16}/></button></>}
        </div>
      </div>
      <aside className="exercise-aside"><div className="aside-progress"><span className="section-kicker">ТВОЙ МАРШРУТ</span><div className="route-numbers"><b>{String(props.active.id).padStart(3,"0")}</b><span>из 520</span></div><div className="route-bar"><i style={{width:Math.min(100,props.completed.length/520*100)+"%"}}/></div><p>{props.completed.length} текстов позади. Ещё один — и серия продолжается.</p></div><div className="aside-milestone"><span className="milestone-star">✳</span><b>Следующий рубеж</b><strong>{props.completed.length<10?"10 искр":props.completed.length<50?"50 текстов":"новый уровень"}</strong><small>Награды остаются тихими. Ты здесь ради практики.</small></div><button className="aside-schulte" onClick={()=>window.dispatchEvent(new CustomEvent("open-schulte"))}><Grid2X2 size={18}/><span><b>Сменить ритм?</b><small>Открой таблицу Шульте</small></span><ArrowUpRight size={17}/></button></aside>
    </div>:<div className="exercise-catalog">
      <div className="catalog-controls"><div className="category-tabs">{categories.map((item)=><button key={item} className={category===item?"category-active":""} onClick={()=>{setCategory(item);setVisibleCount(40);}}>{item}</button>)}</div><label className="search-field"><span>⌕</span><input value={query} onChange={(e)=>{setQuery(e.target.value);setVisibleCount(40);}} placeholder="Найти текст"/></label></div>
      <div className="catalog-summary"><span><b>{filtered.length}</b> упражнений</span><span><Sparkles size={15}/> Сложность растёт каждые 80 текстов</span></div>
      <div className="exercise-list">{filtered.slice(0,visibleCount).map((item)=><button key={item.id} className="exercise-row" onClick={()=>props.onSelect(item.id)}><span className={"exercise-index "+(props.completed.includes(item.id)?"index-complete":"")}>{props.completed.includes(item.id)?<Check size={15}/>:String(item.id).padStart(3,"0")}</span><span className="exercise-row-info"><b>{item.title}</b><small>{item.category} · Уровень {item.level}</small></span><i className={"row-level level-dot-"+item.level}/><span className="row-arrow"><ArrowUpRight size={17}/></span></button>)}</div>
      {visibleCount<filtered.length&&<button className="load-more" onClick={()=>setVisibleCount(visibleCount+40)}>Показать ещё 40 <ChevronDown size={16}/></button>}
      {!props.exercises.length&&<div className="empty-state"><BookOpen size={26}/><p>Подключаем библиотеку…</p></div>}
    </div>}
  </section>;
}
function AccessGate(props:{guest:boolean;onAction:()=>void}) {
  return <section className="locked-page section-wrap"><div className="locked-card"><span className="locked-icon"><LockKeyhole size={24}/></span><span className="section-kicker">{props.guest?"БЕСПЛАТНЫЙ ДЕНЬ":"ТВОЯ ПРАКТИКА ЖДЁТ"}</span><h1>{props.guest?<>Открой свой<br/>первый день.</>:<>Сделай паузу<br/>и вернись завтра.</>}</h1><p>{props.guest?"Создай аккаунт, чтобы включить бесплатные 24 часа и сохранить свой прогресс.":"Оформи доступ на 30 дней, чтобы продолжить с того же задания. Твой прогресс останется с тобой."}</p><button className="button button-dark" onClick={props.onAction}>{props.guest?"Начать бесплатно":"Продлить за 150 ₽"} <ArrowRight size={17}/></button></div></section>;
}
function SchultePage(props:{best:number|null;onComplete:(seconds:number)=>void}) {
  const [grid,setGrid]=useState<number[]>([]),[target,setTarget]=useState(1),[running,setRunning]=useState(false);
  const [finished,setFinished]=useState(false),[startAt,setStartAt]=useState(0),[elapsed,setElapsed]=useState(0),[mistakes,setMistakes]=useState(0);
  const [best,setBest]=useState(props.best);
  const interval=useRef<number|null>(null);
  function shuffle() {
    const values=Array.from({length:25},(_,i)=>i+1);
    for(let i=values.length-1;i>0;i--){const j=Math.floor(Math.random()*(i+1));[values[i],values[j]]=[values[j],values[i]];}
    setGrid(values);setTarget(1);setRunning(false);setFinished(false);setElapsed(0);setMistakes(0);
    if(interval.current)window.clearInterval(interval.current);
  }
  useEffect(()=>{shuffle();return()=>{if(interval.current)window.clearInterval(interval.current);};},[]);
  useEffect(()=>{const handler=()=>window.scrollTo({top:0,behavior:"smooth"});window.addEventListener("open-schulte",handler);return()=>window.removeEventListener("open-schulte",handler);},[]);
  function tap(value:number) {
    if(finished)return;
    if(!running){const now=Date.now();setRunning(true);setStartAt(now);interval.current=window.setInterval(()=>setElapsed(Date.now()-now),50);}
    if(value!==target){setMistakes(mistakes+1);return;}
    if(value===25){
      const seconds=Math.round((Date.now()-(running?startAt:Date.now()))/100)/10;
      setElapsed(seconds*1000);setRunning(false);setFinished(true);
      if(interval.current)window.clearInterval(interval.current);
      props.onComplete(seconds);if(!best||seconds<best)setBest(seconds);
    }else setTarget(value+1);
  }
  const seconds=elapsed/1000;
  return <section className="schulte-page section-wrap">
    <div className="page-topline"><div><span className="section-kicker">ШИРОКИЙ ВЗГЛЯД · СПОКОЙНЫЙ ТЕМП</span><h1>Таблица Шульте</h1><p>Находи числа по порядку, не переводя взгляд слишком резко.</p></div><div className="best-time"><Award size={18}/><span><small>ЛИЧНЫЙ РЕКОРД</small><b>{best?best.toFixed(1)+" сек":"пока пусто"}</b></span></div></div>
    <div className="schulte-layout"><div className="schulte-card"><div className="schulte-card-top"><span className="level-pill level-1"><i/>5 × 5 · классика</span><span className="schulte-target">{finished?"готово":"Ищи: "+target}</span></div>
      <div className="schulte-grid">{grid.map((value,index)=><button key={value} onClick={()=>tap(value)} disabled={finished||value<target} className={"schulte-cell "+(value<target?"cell-done ":"")+(value===target?"cell-target":"")} style={{animationDelay:(index*.025)+"s"}}>{value}</button>)}</div>
      <div className="schulte-footer"><span><Clock3 size={16}/>{seconds.toFixed(1)} сек</span><span><Target size={16}/>Ошибок: {mistakes}</span><button className="button button-dark button-small" onClick={shuffle}>{finished?"Ещё раз":running?"Перемешать":"Новая таблица"}<ArrowRight size={15}/></button></div>
    </div>
    <aside className="schulte-notes"><span className="section-kicker">КАК ИГРАТЬ</span><div className="schulte-step"><span>01</span><p><b>Запусти взгляд</b><small>Нажми на 1, таймер начнётся сам.</small></p></div><div className="schulte-step"><span>02</span><p><b>Найди всё до 25</b><small>Ищи числа по порядку среди остальных.</small></p></div><div className="schulte-step"><span>03</span><p><b>Не гонись за секундомером</b><small>Лучший результат — твой личный ориентир.</small></p></div>
      <div className={"schulte-result "+(finished?"result-done":"")}><span className="result-spark">✳</span>{finished?<><b>Таблица пройдена</b><strong>{seconds.toFixed(1)} сек</strong><small>{mistakes?"Ошибок: "+mistakes+". Попробуй ещё раз ради точности.":"Чистый проход — отлично!"}</small></>:<><b>Маленькая практика</b><small>Одна таблица занимает меньше минуты.</small></>}</div>
    </aside></div>
  </section>;
}

function ProgressPage(props:{user:User|null;completed:number[];streak:number;best:number|null;onPay:()=>void;onAuth:()=>void;onLogout:()=>void;onStart:()=>void}) {
  const percent=Math.min(100,Math.round(props.completed.length/520*100));
  const until=props.user?.accessUntil?new Date(props.user.accessUntil).toLocaleDateString("ru-RU",{day:"numeric",month:"long"}):"";
  return <section className="progress-page section-wrap">
    <div className="page-topline"><div><span className="section-kicker">ТВОЙ РИТМ, ТВОИ ПРАВИЛА</span><h1>Прогресс</h1><p>Смотри на путь, который уже прошёл. Не нужно сравнивать его ни с чьим ещё.</p></div>{props.user?<button className="quiet-button" onClick={props.onLogout}><LogOut size={16}/> Выйти</button>:<button className="button button-dark" onClick={props.onAuth}>Создать аккаунт <ArrowRight size={16}/></button>}</div>
    {!props.user&&<div className="guest-notice"><span className="notice-icon"><LockKeyhole size={19}/></span><div><b>Сейчас ты в гостевом режиме</b><p>Создай аккаунт, чтобы включить бесплатный день и сохранить результаты на всех устройствах.</p></div><button className="text-button text-button-dark" onClick={props.onAuth}>Зарегистрироваться <ArrowRight size={15}/></button></div>}
    <div className="dashboard-grid"><div className="dashboard-main">
      <div className="progress-hero-card"><div className="progress-card-text"><span className="section-kicker">ТВОЯ БИБЛИОТЕКА</span><strong>{props.completed.length}<small> / 520</small></strong><p>заданий пройдено</p><div className="big-progress"><i style={{width:percent+"%"}}/></div><span className="progress-percent">{percent}% пути</span></div><div className="progress-orbit"><div><span>{props.completed.length?percent+"%":"✳"}</span><small>{props.completed.length?"ПРОГРЕСС":"НАЧАЛО"}</small></div></div><span className="card-star">✳</span></div>
      <div className="dashboard-stats"><div className="dash-stat"><span className="dash-icon dash-lime"><Flame size={18}/></span><small>СЕРИЯ ДНЕЙ</small><strong>{props.streak}</strong><span>{props.streak?"ритм держится":"начни сегодня"}</span></div><div className="dash-stat"><span className="dash-icon dash-peach"><Sparkles size={18}/></span><small>СОБРАНО ИСКР</small><strong>{props.completed.length}</strong><span>одна за задачу</span></div><div className="dash-stat"><span className="dash-icon dash-blue"><Clock3 size={18}/></span><small>ШУЛЬТЕ · ЛУЧШЕЕ</small><strong>{props.best?props.best.toFixed(1):"—"}</strong><span>{props.best?"секунд":"ещё впереди"}</span></div></div>
      <div className="recent-card"><div className="recent-head"><div><span className="section-kicker">ПОСЛЕДНИЙ ШАГ</span><h3>{props.completed.length?"Задание №"+String(props.completed[props.completed.length-1]).padStart(3,"0"):"Начни с первой строки"}</h3></div><span className="recent-check"><Check size={17}/></span></div><p>{props.completed.length?"Маленький шаг тоже считается. Продолжай, когда захочешь.":"Прочитай текст и найди спрятанный образ."}</p><button className="text-button text-button-dark" onClick={props.onStart}>Продолжить маршрут <ArrowRight size={16}/></button></div>
    </div><aside className="subscription-card"><div className="subscription-header"><span className="section-kicker">ТВОЙ ДОСТУП</span><span className={"subscription-status "+(props.user?.accessActive?"status-active":"")}><i/>{props.user?(props.user.accessActive?"Активен":"Завершён"):"Не оформлен"}</span></div><div className="subscription-price"><strong>150 ₽</strong><span>/ 30 дней</span></div><p>Полный доступ ко всем текстам и таблицам. Без автоматического списания.</p><div className="subscription-divider"/>
      <div className="subscription-includes"><span><Check size={15}/>520 текстов и мини-историй</span><span><Check size={15}/>4 уровня сложности</span><span><Check size={15}/>Прогресс и серия дней</span><span><Check size={15}/>Таблица Шульте 5 × 5</span></div>
      <button className="button button-dark subscription-button" onClick={props.user?props.onPay:props.onAuth}>{props.user?(props.user.accessActive?"Продлить доступ":"Открыть доступ"):"Начать с бесплатного дня"} <ArrowRight size={16}/></button>
      <small className="trial-note">{props.user?"Доступ до "+until:"1 день бесплатно, затем 150 ₽ за 30 дней"}</small><div className="subscription-footer"><LockKeyhole size={13}/><span>Оплата подтверждается ЮMoney</span></div>
    </aside></div>
  </section>;
}

function AuthModal(props:{mode:AuthMode;onMode:(mode:AuthMode)=>void;onClose:()=>void;onAuthenticated:(user:User)=>void}) {
  const [email,setEmail]=useState(""),[password,setPassword]=useState(""),[code,setCode]=useState("");
  const [verify,setVerify]=useState(false),[error,setError]=useState(""),[busy,setBusy]=useState(false);
  async function submit(event:FormEvent) {
    event.preventDefault();setError("");setBusy(true);
    try {
      if(verify) {
        const data=await api<{user:User}>("/api/verify-email",{method:"POST",body:JSON.stringify({email,code})});props.onAuthenticated(data.user);
      } else {
        const data=await api<{user?:User;needsVerification?:boolean}>("/api/"+(props.mode==="login"?"login":"register"),{method:"POST",body:JSON.stringify({email,password})});
        if(data.needsVerification)setVerify(true);else if(data.user)props.onAuthenticated(data.user);
      }
    } catch(err) {setError(err instanceof Error?err.message:"Не получилось войти.");} finally {setBusy(false);}
  }
  return <div className="modal-backdrop" onMouseDown={(event)=>{if(event.target===event.currentTarget)props.onClose();}}><section className="auth-modal" role="dialog" aria-modal="true" aria-labelledby="auth-title">
    <button className="modal-close" onClick={props.onClose} aria-label="Закрыть"><X size={19}/></button><div className="auth-logo"><span className="brand-mark"><span>ле</span><b>т</b></span></div>
    <span className="section-kicker">{verify?"ПОЧТИ ГОТОВО":props.mode==="login"?"СНОВА РАДЫ ТЕБЕ":"ОДИН ДЕНЬ В ПОДАРОК"}</span>
    <h2 id="auth-title">{verify?"Проверь почту":props.mode==="login"?"Продолжим?":"Начни в своём ритме."}</h2>
    <p>{verify?"Мы отправили код на "+email+". Введи его ниже.":props.mode==="login"?"Войди в аккаунт и продолжай с того места, где остановился.":"Создай аккаунт — первый день тренировок бесплатно."}</p>
    <form onSubmit={submit}>
      {!verify&&<label className="form-label">Почта<input type="email" autoComplete="email" required value={email} onChange={(e)=>setEmail(e.target.value)} placeholder="you@example.com"/></label>}
      {verify?<label className="form-label">Код из письма<input inputMode="numeric" autoComplete="one-time-code" required value={code} onChange={(e)=>setCode(e.target.value)} placeholder="000000" maxLength={6}/></label>:<label className="form-label">Пароль<input type="password" autoComplete={props.mode==="login"?"current-password":"new-password"} required minLength={8} value={password} onChange={(e)=>setPassword(e.target.value)} placeholder="Не менее 8 символов"/></label>}
      {error&&<div className="form-error">{error}</div>}
      <button className="button button-dark auth-submit" disabled={busy}>{busy?"Подожди…":verify?"Подтвердить почту":props.mode==="login"?"Войти":"Создать аккаунт"} <ArrowRight size={16}/></button>
    </form>
    {!verify&&<div className="auth-switch">{props.mode==="login"?"Впервые здесь?":"Уже есть аккаунт?"}<button onClick={()=>{props.onMode(props.mode==="login"?"register":"login");setError("");}}>{props.mode==="login"?"Создать аккаунт":"Войти"}</button></div>}
    <div className="auth-terms"><LockKeyhole size={13}/>Твой прогресс хранится в твоём аккаунте.</div>
  </section></div>;
}
