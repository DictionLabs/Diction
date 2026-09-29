package core

import "strings"

// commonEnglishWords is the 6,000 most frequent words of the keyboard's own shipped English
// dictionary (ios/DictionKeyboard/Resources/TrieDict/trie_en.dtri), extracted once and written
// out literally.
//
// It answers the one question phonetic distance cannot. "park" and "Parker" sit exactly as far
// apart as "Mahala" and "Machala" — 0.25 under the fold — so no threshold can admit the name
// while rejecting the ordinary word. What separates them is that one is a word of the language
// and the other is not, and the autocorrect engine's dictionary already knows which.
//
// Used only to protect a *single* transcript token from being replaced by a different custom
// word. An exact phonetic match still wins, so a user whose My Word genuinely is an ordinary
// word keeps it, and multi-token n-grams are never protected — "set in" → "Vsetín" is exactly
// the mishearing this feature exists for.
//
// English only. Other languages are simply unprotected here, as they are today.
const commonEnglishWordList = "" +
	"ab abandon abandoned abdominal abilities ability able abnormal about above abraham abroad " +
	"absence absent absolute absolutely absorbed absorption abstract abundance abundant abuse ac " +
	"academic academy accept acceptable acceptance accepted accepting access accessible accident " +
	"accommodation accompanied accompanying accomplish accomplished accord accordance according " +
	"accordingly account accounted accounting accounts accumulated accumulation accuracy accurate " +
	"accurately accused accustomed achieve achieved achievement achieving acid acids acknowledge " +
	"acknowledged acquaintance acquainted acquire acquired acquisition acre acres across act acted " +
	"acting action actions activation active actively activities activity actor actors acts actual " +
	"actually acute ad adam adams adaptation adapted add added adding addition additional address " +
	"addressed addresses adds adequate adjacent adjusted adjustment administered administration " +
	"administrative admiral admiration admission admit admitted adopt adopted adoption adult adults " +
	"advance advanced advances advantage advantages adventure adverse advertising advice advised " +
	"advocate aesthetic affair affairs affect affected affecting affection affects afford afraid " +
	"africa african after afternoon afterward afterwards again against age aged agencies agency " +
	"agenda agent agents ages aggregate aggression aggressive aging ago agree agreed agreement " +
	"agreements agricultural agriculture ah ahead aid aids aim aimed aims air aircraft al alarm " +
	"albert alcohol alexander alfred algorithm alice alien alike alive all alleged allen alliance " +
	"allied allies allocation allow allowance allowed allowing allows almost alone along already also " +
	"altar alter altered alternative alternatives although altogether always am ambassador ambition " +
	"amended amendment america american americans amino among amount amounts ample an analogy " +
	"analyses analysis analytical analyzed ancestors ancient and anderson andrew angel angeles angels " +
	"anger angle angles anglo angry animal animals ann anna anne announced annual annually another " +
	"answer answered answers anterior anthony anti anticipated antonio anxiety anxious any anybody " +
	"anyone anything anyway anywhere apart apartment apparatus apparent apparently appeal appeals " +
	"appear appearance appeared appearing appears appendix apple applicable application applications " +
	"applied applies apply applying appointed appointment appreciate appreciation approach approached " +
	"approaches approaching appropriate approval approved approximate approximately april apt arab " +
	"arbitrary arc arch archbishop architectural architecture archives are area areas argue argued " +
	"argues argument arguments arise arises arising aristotle arm armed armies arms army arnold arose " +
	"around arrange arranged arrangement arrangements array arrest arrested arrival arrive arrived " +
	"arrow art artery arthur article articles artificial artillery artist artistic artists arts as " +
	"asia asian aside ask asked asking asks asleep aspect aspects assault assembled assembly assert " +
	"asserted assertion assess assessment asset assets assigned assignment assist assistance " +
	"assistant assisted associate associated associates association associations assume assumed " +
	"assumes assuming assumption assumptions assurance assure assured at ate athens atlantic " +
	"atmosphere atom atomic atoms attached attachment attack attacked attacks attain attained attempt " +
	"attempted attempting attempts attend attendance attended attending attention attitude attitudes " +
	"attorney attract attracted attraction attractive attribute attributed attributes au audience " +
	"august aunt australia australian austria author authorities authority authorized authors " +
	"automatic automatically automobile autonomy autumn availability available avenue average avoid " +
	"avoided award aware awareness away axis baby back background backward bacon bacteria bad badly " +
	"bag baker balance balanced ball baltimore band bands bank banking banks bar barbara bare barely " +
	"bargaining barrier barriers bars base based bases basic basically basin basis bath battery " +
	"battle bay be beach beam bear bearing bears beat beautiful beauty became because become becomes " +
	"becoming bed bedroom beds been beer before began begin beginning begins begun behalf behavior " +
	"behavioral behaviors behind being beings belief beliefs believe believed believes believing bell " +
	"belong belonged belonging belongs beloved below belt ben bench beneath beneficial benefit " +
	"benefits benjamin bent berkeley berlin bernard beside besides best better between beyond bias " +
	"bible biblical bibliography bid big bill billion bills binding biography biol biological biology " +
	"bird birds birth bishop bishops bit bits bitter black blacks blame blank bleeding blessed " +
	"blessing blind block blocks blood bloody blow blue board boards boat boats bob bodies bodily " +
	"body boiling bold bond bonds bone bones book books border borders bore born borne borrowed " +
	"boston both bottle bottom bought bound boundaries boundary bow bowl box boxes boy boys brain " +
	"branch branches brand brass brave brazil breach bread break breakfast breaking breaks breast " +
	"breath breathing breeding brethren brick bride bridge brief briefly bright brilliant bring " +
	"bringing brings britain british broad broader broke broken bronze brother brothers brought brown " +
	"brush budget build building buildings built bulk bull bulletin burden bureau buried burn burned " +
	"burning burst bus bush business businesses busy but butler butter button buy buyer buying by ca " +
	"cabin cabinet cable cal calcium calculate calculated calculation calculations california call " +
	"called calling calls calm cambridge came camera camp campaign campbell camps campus can canada " +
	"canadian canal cancer candidate candidates cap capabilities capable capacity cape capital " +
	"capitalism capitalist captain capture captured car carbon card cardiac cardinal cards care " +
	"career careful carefully carl carolina carriage carried carrier carries carry carrying cars " +
	"carter case cases cash cast castle cat catch categories category cathedral catherine catholic " +
	"catholics cattle caught cause caused causes causing caution cavalry cave cavity cd ce cease " +
	"ceased ceiling celebrated cell cells cellular cement census cent center centered centers central " +
	"cents centuries century cerebral ceremony certain certainly certainty certificate cf ch chain " +
	"chains chair chairman challenge challenges chamber chambers chance chancellor chances change " +
	"changed changes changing channel channels chap chapel chapter chapters character characteristic " +
	"characteristics characterized characters charge charged charges charity charles charm chart " +
	"charter chase cheap check checked checking cheese chem chemical chemicals chemistry chest " +
	"chicago chicken chief chiefly chiefs child childhood children china chinese chloride choice " +
	"choices choose choosing chose chosen christ christian christianity christians christmas chronic " +
	"church churches churchill circle circles circuit circular circulation circumstance circumstances " +
	"cit cited cities citizen citizens citizenship city civil civilization claim claimed claims clark " +
	"class classes classic classical classification classified classroom clause clay clean cleaning " +
	"clear cleared clearly clergy clerk click client clients climate clinical clock close closed " +
	"closely closer closing cloth clothes clothing cloud clouds club clubs cluster cm co coach coal " +
	"coalition coarse coast coastal coat code codes coefficient coefficients coffee cognitive cold " +
	"collapse colleagues collect collected collecting collection collections collective college " +
	"colleges colonel colonial colonies colony color colorado colored colors columbia column columns " +
	"com combat combination combinations combine combined come comedy comes comfort comfortable " +
	"coming command commanded commander commands commenced comment commentary comments commerce " +
	"commercial commission commissioner commissioners commit commitment committed committee " +
	"committees commodities commodity common commonly commons commonwealth communicate communication " +
	"communications communist communists communities community companies companion companions company " +
	"comparable comparative comparatively compare compared comparing comparison comparisons compelled " +
	"compensation competence competent competition competitive complained complaint complaints " +
	"complete completed completely completion complex complexity compliance complicated complications " +
	"component components composed composition compound compounds comprehensive compression " +
	"compromise computed computer computers con conceived concentrate concentrated concentration " +
	"concentrations concept conception concepts conceptual concern concerned concerning concerns " +
	"conclude concluded conclusion conclusions concrete condemned condition conditions conduct " +
	"conducted conducting conference confidence confident configuration confined confirm confirmed " +
	"conflict conflicts confused confusion congregation congress congressional conjunction connect " +
	"connected connecticut connection connections conquest conscience conscious consciousness " +
	"consensus consent consequence consequences consequently conservation conservative consider " +
	"considerable considerably consideration considerations considered considering considers consist " +
	"consisted consistency consistent consistently consisting consists constant constantly constitute " +
	"constituted constitutes constitution constitutional constraints construct constructed " +
	"construction consumed consumer consumers consumption contact contacts contain contained " +
	"containing contains contemporary contempt content contents contest context continent continental " +
	"continually continue continued continues continuing continuity continuous contract contracts " +
	"contrary contrast contribute contributed contribution contributions control controlled " +
	"controlling controls controversy convenient convention conventional conventions conversation " +
	"conversion convert converted convey conviction convinced cook cooking cool cooling cooper " +
	"cooperation cooperative coordination copies copper copy copyright cord core corn corner " +
	"corporate corporation corporations corps correct correction correctly correlation correspond " +
	"correspondence corresponding corresponds corruption cost costs cotton could council councils " +
	"counsel count counted counter counties countries country county couple coupled courage course " +
	"courses court courtesy courts cousin covenant cover coverage covered covering covers cow craft " +
	"cream create created creates creating creation creative creature creatures credit creek crew " +
	"cried crime crimes criminal crisis criteria criterion critic critical criticism critics critique " +
	"crop crops cross crossed crossing crowd crowded crown crucial crude cruel cry crying crystal " +
	"crystals ct cuba cultivated cultivation cultural culture cultures cup cure curiosity curious " +
	"currency current currently currents curriculum curve curves custom customary customer customers " +
	"customs cut cuts cutting cycle cycles cylinder da dad daily damage damaged damages dan dance " +
	"dancing danger dangerous dangers daniel dare dark darkness data database date dated dates " +
	"daughter daughters david davis dawn day days dc de dead deal dealing deals dealt dean dear death " +
	"deaths debate debt debts decade decades decay december decide decided decision decisions " +
	"decisive deck declaration declare declared decline declined decrease decreased decreases decree " +
	"dedicated deed deeds deemed deep deeper deeply deer default defeat defeated defect defects " +
	"defend defendant defense deficiency define defined defining definite definitely definition " +
	"definitions degree degrees del delay delayed delegates delhi deliberately delicate delight " +
	"deliver delivered delivery demand demanded demands democracy democratic democrats demonstrate " +
	"demonstrated demonstration den denial denied dense density deny department departments departure " +
	"depend dependence dependent depending depends deposit deposited deposits depressed depression " +
	"deprived depth depths deputy derive derived descent describe described describes describing " +
	"description descriptions desert design designated designed designs desirable desire desired " +
	"desires desk despair desperate despite destination destiny destroy destroyed destruction " +
	"destructive detail detailed details detect detected detection determination determine determined " +
	"determines determining develop developed developing development developmental developments " +
	"develops deviation device devices devil devoted devotion di diagnosis diagnostic diagram " +
	"dialogue diameter diary dictionary did die died dies diet differ difference differences " +
	"different differential differentiation differently differs difficult difficulties difficulty " +
	"diffusion digital dignity dimension dimensional dimensions diminished dining dinner diplomatic " +
	"direct directed direction directions directly director directors directory dirty disability " +
	"disappear disappeared disaster discharge disciples discipline discourse discover discovered " +
	"discovery discretion discrimination discuss discussed discussing discussion discussions disease " +
	"diseases disk dismissed disorder disorders displacement display displayed displays disposal " +
	"disposed disposition dispute disputes dissolved distance distances distant distinct distinction " +
	"distinctive distinctly distinguish distinguished distress distributed distribution district " +
	"districts disturbance disturbed diverse diversity divide divided divine division divisions " +
	"divorce dna do doctor doctors doctrine doctrines document documents does dog dogs doing dollar " +
	"dollars domain domestic dominant dominated don donald done door doors dose doses double doubt " +
	"doubtful doubtless doubts douglas down dozen dr draft drainage drama dramatic draw drawing " +
	"drawings drawn dream dreams dress dressed drew dried drink drinking drive driven driver driving " +
	"drop dropped drops drove drug drugs dry du dual dublin due duke dull duration during dust dutch " +
	"duties duty dwelling dying dynamic dynamics each eager ear earl earlier earliest early earn " +
	"earned earnest earnings ears earth ease easier easily east eastern easy eat eating " +
	"ecclesiastical economic economics economies economy ed edge edges edinburgh edited edition " +
	"editor editorial editors eds educated education educational edward effect effected effective " +
	"effectively effectiveness effects efficiency efficient effort efforts egg eggs ego egypt " +
	"egyptian eight eighteen eighteenth eighth eighty either elaborate elastic elder elderly elected " +
	"election elections electric electrical electricity electron electronic electrons element " +
	"elementary elements elevated elevation eleven eliminate eliminated elite elizabeth else " +
	"elsewhere em emerge emerged emergence emergency emerging eminent emotion emotional emotions " +
	"emperor emphasis emphasize emphasized empire empirical employ employed employee employees " +
	"employer employers employment empty en enable enabled enables enacted encounter encountered " +
	"encourage encouraged encouragement encouraging end ended ending endless ends enemies enemy " +
	"energies energy enforcement engage engaged engagement engine engineer engineering engineers " +
	"engines england english enhance enhanced enjoy enjoyed enjoyment enlarged enormous enough ensure " +
	"enter entered entering enterprise enterprises enters entertainment enthusiasm entire entirely " +
	"entitled entity entrance entries entry environment environmental environments enzyme episode " +
	"equal equality equally equation equations equilibrium equipment equipped equity equivalent er " +
	"era erected error errors es escape escaped especially essay essays essence essential essentially " +
	"est establish established establishing establishment estate estates esteem estimate estimated " +
	"estimates estimation et eternal ethical ethics ethnic europe european evaluate evaluated " +
	"evaluation eve even evening event events eventually ever every everybody everyday everyone " +
	"everything everywhere evidence evident evidently evil evolution evolved ex exact exactly " +
	"examination examine examined examining example examples exceed excellent except exception " +
	"exceptional exceptions excess excessive exchange excited excitement exciting exclaimed excluded " +
	"exclusion exclusive exclusively excuse executed execution executive exercise exercised exercises " +
	"exhausted exhibit exhibited exhibition exist existed existence existing exists exit expand " +
	"expanded expanding expansion expect expectation expectations expected expedition expenditure " +
	"expenditures expense expenses expensive experience experienced experiences experiment " +
	"experimental experiments expert experts explain explained explaining explains explanation " +
	"explanations explicit explicitly exploitation exploration explore export exports exposed " +
	"exposure express expressed expressing expression expressions extend extended extending extends " +
	"extension extensive extent external extra extract extraordinary extreme extremely eye eyes " +
	"fabric face faced faces facilitate facilities facility facing fact factor factories factors " +
	"factory facts faculty fail failed failing fails failure faint fair fairly faith faithful fall " +
	"fallen falling falls false fame familiar families family famous fancy far farm farmer farmers " +
	"farming farms farther fashion fast faster fat fatal fate father fathers fatigue fault favor " +
	"favorable favorite fear feared fears feature features february fed federal federation fee feed " +
	"feedback feeding feel feeling feelings feels fees feet fell fellow felt female females feminist " +
	"fertility festival fever few fewer ff fiber fibers fiction field fields fifteen fifth fifty fig " +
	"fight fighting figure figures file filed files fill filled filling film films filter final " +
	"finally finance financial financing find finding findings finds fine finest finger fingers " +
	"finish finished finite fire fired fires firing firm firmly firms first fiscal fish fishing fit " +
	"fitted fitting five fix fixed flag flame flash flat fled fleet flesh flexibility flexible flight " +
	"floating flood floor florence florida flour flow flower flowers flowing flows fluid flux fly " +
	"flying focus focused fold folk follow followed followers following follows fond food foods fool " +
	"foot for forbidden force forced forces ford foreign forest forests forever forget forgotten form " +
	"formal formally format formation formed former formerly forming forms formula formulation fort " +
	"forth fortunately fortune forty forward foster fought found foundation foundations founded " +
	"founder four fourteen fourth fox fraction fracture fragments frame framework france francis " +
	"francisco frank franklin frederick free freedom freely french frequencies frequency frequent " +
	"frequently fresh freud friction friday friend friendly friends friendship frightened from front " +
	"frontier frozen fruit fruits ft fuel full fully fun function functional functioning functions " +
	"fund fundamental funding funds funeral fur furnish furnished furniture further furthermore " +
	"future gain gained gains gallery game games gap garden gardens gas gases gate gates gather " +
	"gathered gathering gave gay gaze gender gene general generally generate generated generation " +
	"generations generous genes genetic genius gentle gentleman gentlemen gently genuine geographical " +
	"geography geometry george georgia german germans germany gesture get gets getting ghost giant " +
	"gift gifts girl girls give given gives giving glad glance glass glasses global globe glorious " +
	"glory glucose go goal goals god gods goes going gold golden gone good goodness goods gordon " +
	"gospel got governed governing government governmental governments governor grace grade grades " +
	"gradual gradually graduate grain grains grammar grand grandfather grant granted grants graph " +
	"grasp grass grateful gratitude grave gravity gray great greater greatest greatly greece greek " +
	"greeks green grew grey grid grief gross ground grounds group groups grow growing grown grows " +
	"growth guarantee guard guards guess guest guests guidance guide guided guidelines guilt guilty " +
	"gulf gun guns guy ha habit habits had hair half hall hamilton hand handbook handed handle " +
	"handled handling hands handsome hang hanging happen happened happening happens happiness happy " +
	"harbor hard harder hardly hardware harm harmony harold harper harris harry harvard harvest has " +
	"hat hate hath hatred have haven having hay he head headed heading headquarters heads healing " +
	"health healthy hear heard hearing heart hearts heat heated heating heaven heavily heavy hebrew " +
	"height heights held helen hell help helped helpful helping helps hence henry her herbert here " +
	"heritage hero herself hi hidden hide hierarchy high higher highest highly highway hill hills him " +
	"himself hindu hired his historian historians historic historical history hit hitherto hitler ho " +
	"hold holding holds hole holes holland hollow holy home homes hon honest honey hong honor hope " +
	"hoped hopes hoping horizon horizontal horn horror horse horses hospital hospitals host hostile " +
	"hostility hot hotel hour hours house household households houses housing how howard however huge " +
	"human humanity humans humble humor hundred hundreds hung hungary hunger hungry hunt hunter " +
	"hunting hurt husband hydrogen hypothesis ibid ice id idea ideal ideals ideas identical " +
	"identification identified identify identifying identity ideological ideology if ignorance " +
	"ignorant ignore ignored ii iii il ill illegal illinois illness illustrate illustrated " +
	"illustrates illustration illustrations image images imagination imagine imagined immediate " +
	"immediately immense immigrants immigration immune impact imperial implement implementation " +
	"implemented implications implicit implied implies imply import importance important imported " +
	"imports impose imposed impossible impressed impression impressions impressive improve improved " +
	"improvement improvements improving impulse in inability inadequate inch inches incidence " +
	"incident incidents inclined include included includes including income incomplete incorporated " +
	"increase increased increases increasing increasingly indeed independence independent " +
	"independently index india indian indiana indians indicate indicated indicates indicating " +
	"indication indigenous indirect indirectly individual individuals induce induced induction " +
	"industrial industries industry inevitable inevitably infant infantry infants infected infection " +
	"infections inferior infinite inflation influence influenced influences influential inform " +
	"informal information informed ing inhabitants inherent inheritance inherited initial initially " +
	"initiated initiative injection injured injuries injury inn inner innocent innovation input " +
	"inputs inquiry insects inserted inside insight insisted inspection inspiration inspired " +
	"installed instance instances instant instantly instead instinct institute institution " +
	"institutional institutions instructed instruction instructions instrument instruments " +
	"insufficient insurance int integral integrated integration integrity intellect intellectual " +
	"intelligence intelligent intended intense intensity intensive intent intention intentions inter " +
	"interaction interactions intercourse interest interested interesting interests interface " +
	"interfere interference interior intermediate internal international internet interpret " +
	"interpretation interpretations interpreted interrupted interval intervals intervention interview " +
	"interviews intimate into introduce introduced introducing introduction invariably invasion " +
	"invented invention inventory invested investigate investigated investigation investigations " +
	"investment investments investors invisible invitation invited involve involved involvement " +
	"involves involving ion ions iowa ireland irish iron irregular irrigation is islam islamic island " +
	"islands isolated isolation israel issue issued issues it italian italy item items its itself iv " +
	"ix jack jackson jacob james jane january japan japanese jean jefferson jersey jerusalem jesus " +
	"jewish jews jim job jobs joe john johnson join joined joint joints jones jordan joseph journal " +
	"journals journey joy juan judge judged judges judgment judgments judicial juice july jump " +
	"junction june junior jurisdiction jury just justice justification justified justify kansas karl " +
	"keen keep keeping keeps kennedy kentucky kept key keys kg kidney kids kill killed killing kind " +
	"kindly kindness kinds king kingdom kings kiss kitchen km knee knees knew knife knight know " +
	"knowing knowledge known knows kong korea korean la label labor laboratory lack lacking ladies " +
	"lady laid lake lakes lamp land landed landing lands landscape lane language languages large " +
	"largely larger largest las laser last lasted lasting late lately later lateral latest latin " +
	"latter laugh laughed laughing laughter launched law lawrence laws lawyer lawyers lay layer " +
	"layers laying le lead leader leaders leadership leading leads leaf league learn learned learning " +
	"lease least leather leave leaves leaving lecture lectures led lee left leg legal legend " +
	"legislation legislative legislature legitimate legs leisure length lens les lesions less lesser " +
	"lesson lessons lest let letter letters letting level levels lewis li liability liable liberal " +
	"liberation liberty libraries library license lie lies lieutenant life lifetime lift lifted light " +
	"lighting lights like liked likelihood likely likewise lime limit limitation limitations limited " +
	"limiting limits lincoln line linear lines linguistic link linked links lion lips liquid list " +
	"listed listen listened listening lists lit literally literary literature little live lived liver " +
	"lives living ll ln lo load loaded loading loan loans local located location locations lock " +
	"locked lodge log logic logical london long longer look looked looking looks loop loose lord " +
	"lords los lose losing loss losses lost lot lots loud louis louisiana love loved lovely lover " +
	"loves loving low lower lowest loyal loyalty lt luck luke lunch lung lungs luther lying ma " +
	"machine machinery machines macmillan mad madame made madison magazine magazines magic magnetic " +
	"magnificent magnitude mail main mainly maintain maintained maintaining maintenance majesty major " +
	"majority make makers makes making male males man manage managed management manager managers " +
	"managing manifest mankind manner manners manual manufacture manufactured manufacturer " +
	"manufacturers manufacturing manuscript many map maps marble march marched margaret margin " +
	"marginal maria marie marine mark marked market marketing markets marks marriage married marry " +
	"marshall martin marx mary maryland mask mass massachusetts masses massive master masters match " +
	"material materials maternal mathematical mathematics matrix matter matters matthew mature " +
	"maturity max maximum may maybe mayor me meal mean meaning meaningful meanings means meant " +
	"meanwhile measure measured measurement measurements measures measuring meat mechanical mechanics " +
	"mechanism mechanisms med media median medical medicine medieval mediterranean medium meet " +
	"meeting meetings meets member members membership membrane memorial memories memory men mental " +
	"mention mentioned menu merchant merchants mercury mercy mere merely merit merits message " +
	"messages met metabolism metal metals method methods metropolitan mexican mexico mg mi mice " +
	"michael michigan mid middle midnight midst might mighty migration mild mile miles military milk " +
	"mill miller million millions mills milton min mind minded minds mine mineral minerals mines " +
	"minimal minimum mining minister ministers ministry minnesota minor minority minute minutes " +
	"mirror misery miss missed missing mission missionaries missionary missions mississippi missouri " +
	"mistake mistaken mistakes mit mix mixed mixing mixture ml mm mobile mobility mode model models " +
	"moderate modern modes modest modification modified moisture molecular molecule molecules mom " +
	"moment moments monday monetary money monitor monitoring monopoly month monthly months mood moon " +
	"moore moral morality more moreover morgan morning morris mortality mortgage moscow moses most " +
	"mostly mother mothers motion motivation motive motives motor mount mountain mountains mounted " +
	"mouse mouth move moved movement movements moves movie moving mr mrs ms much mud multi multiple " +
	"municipal murder murray muscle muscles muscular museum music musical muslim muslims must mutual " +
	"my myself mysterious mystery myth na naked name named namely names napoleon narrative narrow " +
	"nation national nationalism nations native natives natural naturally nature naval navigation " +
	"navy ne near nearby nearer nearest nearly necessarily necessary necessity neck need needed " +
	"needle needs negative neglect neglected negotiations negro negroes neighborhood neighbors " +
	"neither nelson nerve nerves nervous nest net netherlands network networks neutral never " +
	"nevertheless new newly news newspaper newspapers newton next nice nicholas night nights nine " +
	"nineteenth nitrogen no noble nobody nodded node nodes noise non none nonetheless nor normal " +
	"normally norman norms north northern northwest nose not notable notably note noted notes nothing " +
	"notice noticed notion notions notwithstanding novel novels november now nowhere nuclear nucleus " +
	"number numbers numerical numerous nurse nurses nursing nutrition ny oak oath obedience object " +
	"objection objections objective objectives objects obligation obligations obliged obscure " +
	"observation observations observe observed observer observing obtain obtained obtaining obvious " +
	"obviously occasion occasional occasionally occasions occupation occupational occupations " +
	"occupied occupy occur occurred occurrence occurring occurs ocean october odd of off offensive " +
	"offer offered offering offers office officer officers offices official officials often oh ohio " +
	"oil okay old older oldest omitted on once one ones online only onset onto op open opened opening " +
	"openly opens opera operate operated operating operation operational operations operative " +
	"operator operators opinion opinions opponents opportunities opportunity opposed opposite " +
	"opposition optical optimal option options or oral orange order ordered orders ordinary ore organ " +
	"organic organism organisms organization organizational organizations organize organized " +
	"organizing organs orientation oriented origin original originally origins orleans orthodox other " +
	"others otherwise ought our ours ourselves out outcome outcomes outer outline outlined outlook " +
	"output outside outstanding outward over overall overcome overseas overview owing own owned owner " +
	"owners ownership oxford oxide oxygen pa pace pacific pack package packed page pages paid pain " +
	"painful pains paint painted painter painting paintings pair pairs pakistan palace pale palestine " +
	"palm pan panel paper papers par para paragraph parallel parameter parameters parent parents " +
	"paris parish park parliament parliamentary part partial partially participants participate " +
	"participation particle particles particular particularly parties partly partner partners " +
	"partnership parts party pass passage passages passed passengers passes passing passion passions " +
	"passive past patent path paths patience patient patients pattern patterns paul pause pay payable " +
	"paying payment payments pays peace peaceful peak peasant peasants peculiar pen penalty " +
	"pennsylvania pension people peoples per perceive perceived percent percentage perception " +
	"perceptions perfect perfection perfectly perform performance performed performing perhaps period " +
	"periods peripheral permanent permission permit permits permitted persian persistent person " +
	"personal personality personally personnel persons perspective perspectives persuaded peter " +
	"petition petroleum ph phase phases phenomena phenomenon philadelphia philip philippines " +
	"philosopher philosophers philosophical philosophy phone photo photograph photographs phrase " +
	"phrases phys physical physically physician physicians physics physiological pi piano pick picked " +
	"picture pictures piece pieces pierre pilot pin pine pink pipe pitch pity place placed places " +
	"placing plain plains plaintiff plan plane planes planet planned planning plans plant planted " +
	"plants plasma plastic plate plates platform plato play played player players playing plays " +
	"pleasant please pleased pleasure plenty plot plus pocket poem poems poet poetic poetry poets " +
	"point pointed pointing points poland pole poles police policies policy polish political " +
	"politically politicians politics pollution pool poor pope popular popularity population " +
	"populations port portion portions portrait ports portuguese position positions positive possess " +
	"possessed possession possibilities possibility possible possibly post posterior posts pot " +
	"potassium potential potentially pound pounds pour poured poverty powder power powerful powers pp " +
	"practical practically practice practices praise pray prayer prayers preceding precious precise " +
	"precisely precision predict predicted prediction preface prefer preference preferences preferred " +
	"pregnancy pregnant prejudice preliminary premises preparation preparations prepare prepared " +
	"preparing prescribed presence present presentation presented presenting presently presents " +
	"preservation preserve preserved president presidential press pressed pressing pressure pressures " +
	"presumably pretty prevailing prevent prevented preventing prevention previous previously price " +
	"prices pride priest priests primarily primary prime primitive prince princes princess princeton " +
	"principal principle principles print printed printing prior priority prison prisoner prisoners " +
	"privacy private privilege privileges prize pro probability probable probably problem problems " +
	"procedure procedures proceed proceeded proceeding proceedings proceeds process processes " +
	"processing produce produced producer producers produces producing product production productive " +
	"productivity products profession professional professionals professor profile profit profitable " +
	"profits profound program programming programs progress progressive project projected projection " +
	"projects prolonged prominent promise promised promises promote promoted promoting promotion " +
	"pronounced proof propaganda proper properly properties property prophet proportion proportional " +
	"proportions proposal proposals propose proposed proposition prose prospect prospects prosperity " +
	"protect protected protection protective protein proteins protest protestant protocol proud prove " +
	"proved provide provided providence provides providing province provinces provincial provision " +
	"provisions psychiatric psychological psychology public publication publications publicly " +
	"published publisher publishers publishing pull pulled pulmonary pulse pump punishment pupil " +
	"pupils purchase purchased purchasing pure purely purity purple purpose purposes pursue pursued " +
	"pursuit push pushed put puts putting qualified qualities quality quantitative quantities " +
	"quantity quantum quarter quarterly quarters que queen question questioned questions quick " +
	"quickly quiet quietly quite quoted race races racial radiation radical radio radius rail " +
	"railroad railway rain raise raised raising ran random range ranges ranging rank ranks rapid " +
	"rapidly rare rarely rat rate rates rather rating ratio rational ratios rats raw ray rays re " +
	"reach reached reaches reaching reaction reactions read reader readers readily reading readings " +
	"reads ready real realistic reality realization realize realized really realm rear reason " +
	"reasonable reasonably reasoning reasons rebellion recall recalled receive received receiver " +
	"receives receiving recent recently reception receptor recognition recognize recognized recommend " +
	"recommendations recommended reconstruction record recorded recording records recover recovered " +
	"recovery red reduce reduced reduces reducing reduction refer reference references referred " +
	"referring refers reflect reflected reflecting reflection reflects reform reforms refusal refuse " +
	"refused regard regarded regarding regardless regards regime regiment region regional regions " +
	"register registered registration regression regret regular regularly regulated regulation " +
	"regulations regulatory reign reinforced reject rejected rejection relate related relates " +
	"relating relation relations relationship relationships relative relatively relatives relaxation " +
	"release released relevant reliability reliable relief relieved religion religions religious rely " +
	"remain remainder remained remaining remains remark remarkable remarked remarks remedy remember " +
	"remembered reminded remote removal remove removed removing renaissance renal render rendered " +
	"renewed rent repair repeat repeated repeatedly repetition replace replaced replacement replied " +
	"reply report reported reporting reports represent representation representations representative " +
	"representatives represented representing represents reprinted reproduced reproduction republic " +
	"republican reputation request requested requests require required requirement requirements " +
	"requires requiring res research researchers reserve reserved reserves residence resident " +
	"residential residents resist resistance resolution resolve resolved resort resource resources " +
	"respect respected respecting respective respectively respects respiratory respond responded " +
	"respondents response responses responsibilities responsibility responsible rest restaurant " +
	"resting restoration restore restored restricted restriction restrictions rests result resulted " +
	"resulting results retail retain retained retired retirement retreat return returned returning " +
	"returns reveal revealed reveals revelation revenue revenues reverse reversed review reviewed " +
	"reviews revised revision revolt revolution revolutionary reward rhetoric rhythm rice rich " +
	"richard rid ride ridge riding right rights rigid ring rings rise rises rising risk risks ritual " +
	"rival river rivers road roads robert robinson rock rocks rod rode roger role roles roll rolled " +
	"rolling roman romance romans romantic rome roof room rooms roosevelt root roots rope rose ross " +
	"rotation rough roughly round rounded route routine row rows roy royal rs rubber ruin rule ruled " +
	"ruler rulers rules ruling run running runs rural rush russell russia russian russians sa sacred " +
	"sacrifice sad safe safely safety said sail saint saints sake salary sale sales salt salvation " +
	"sam same sample samples sampling samuel san sand santa sarah sat satisfaction satisfactory " +
	"satisfied satisfy saturday savage save saved saving savings saw say saying says scale scales " +
	"scarcely scattered scene scenes schedule scheduled scheme schemes scholar scholars scholarship " +
	"school schools sci science sciences scientific scientists scope score scores scotland scott " +
	"scottish screen script scripture se sea seal search searching seas season seat seated seats sec " +
	"second secondary secondly seconds secret secretary section sections sector sectors secular " +
	"secure secured securities security see seed seeds seeing seek seeking seeks seem seemed " +
	"seemingly seems seen sees segment segments seized seldom select selected selecting selection " +
	"selective self sell selling semi senate senator send sending senior sensation sense senses " +
	"sensible sensitive sensitivity sensory sent sentence sentences sentiment sentiments separate " +
	"separated separately separation september sequence sequences series serious seriously sermon " +
	"serum servant servants serve served server serves service services serving session sessions set " +
	"sets setting settings settle settled settlement settlements settlers seven seventeenth seventh " +
	"seventy several severe severely severity sex sexual shade shadow shadows shaft shakespeare shall " +
	"shallow shame shape shaped shapes share shared shares sharing sharp sharply she shed sheep sheet " +
	"sheets shell shelter shift shifted shifts ship shipping ships shirt shock shoes shook shoot " +
	"shooting shop shopping shops shore short shorter shortly shot should shoulder shoulders shouted " +
	"show showed showing shown shows shut si sick side sides sight sign signal signals signed " +
	"significance significant significantly signs silence silent silk silver similar similarity " +
	"similarly simon simple simplicity simply simulation simultaneously sin since sing singing single " +
	"singular sins sir sister sisters sit site sites sitting situated situation situations six " +
	"sixteen sixteenth sixth sixty size sizes sketch skill skilled skills skin sky slave slavery " +
	"slaves sleep sleeping slide slight slightly slip slope slow slowly small smaller smallest smell " +
	"smile smiled smiling smith smoke smoking smooth snow so soc social socialism socialist socially " +
	"societies society sociology sodium soft software soil soils solar sold soldier soldiers sole " +
	"solely solemn solid solution solutions solve solved solving some somebody somehow someone " +
	"something sometimes somewhat somewhere son song songs sons soon sooner sophisticated sorrow " +
	"sorry sort sorts sought soul souls sound sounded sounds source sources south southeast southern " +
	"sovereign sovereignty soviet space spaces spain span spanish spare spatial speak speaker " +
	"speakers speaking speaks special specialized specially species specific specifically " +
	"specification specified specify specimen specimens spectrum speech speeches speed spend spending " +
	"spent sphere spinal spirit spirits spiritual spite splendid split spoke spoken spontaneous sport " +
	"sports spot spots spread spreading spring springs square st stability stable staff stage stages " +
	"stairs stand standard standards standing stands stanford star stared stars start started " +
	"starting starts state stated statement statements states static station stations statistical " +
	"statistics status statute statutes statutory stay stayed steadily steady steam steel steep stem " +
	"step stephen stepped steps stern stick still stimulation stimuli stimulus stock stocks stomach " +
	"stone stones stood stop stopped storage store stored stores stories storm story straight strain " +
	"strains strange stranger strategic strategies strategy stream streams street streets strength " +
	"stress stressed stresses stretch stretched strict strictly strike strikes striking string strip " +
	"stroke strong stronger strongly struck structural structure structures struggle stuck student " +
	"students studied studies studio study studying stuff style styles sub subject subjected " +
	"subjective subjects submit submitted subordinate subsequent subsequently substance substances " +
	"substantial substantially substitute subtle succeed succeeded success successful successfully " +
	"succession successive successor such sudden suddenly suffer suffered suffering sufficient " +
	"sufficiently sugar suggest suggested suggesting suggestion suggestions suggests suicide suit " +
	"suitable suited sum summary summer summit sun sunday superficial superintendent superior " +
	"superiority supervision supper supplement supplied supplies supply support supported supporting " +
	"supports suppose supposed supra supreme sure surely surface surfaces surgeon surgery surgical " +
	"surplus surprise surprised surprising surrender surrounded surrounding survey surveys survival " +
	"survive survived susan suspect suspected suspended suspension suspicion sustained sweden sweet " +
	"swept swift switch switzerland sword symbol symbolic symbols sympathetic sympathy symptoms " +
	"syndrome synthesis system systematic systems table tables tail take taken takes taking tale " +
	"talent tales talk talked talking talks tall tank tape target targets tariff task tasks taste " +
	"taught tax taxation taxes taylor tea teach teacher teachers teaching team teams tears technical " +
	"technique techniques technological technologies technology teeth tel telephone television tell " +
	"telling tells temper temperature temperatures temple temporal temporary ten tend tended " +
	"tendencies tendency tender tends tennessee tension tenth term termed terminal termination terms " +
	"terrible territorial territories territory terror test testament tested testimony testing tests " +
	"texas text texts th than thank thanks that the theater thee their them theme themes themselves " +
	"then thence theological theology theoretical theories theory therapeutic therapist therapy there " +
	"thereafter thereby therefore therein thereof thermal these thesis they thick thickness thin " +
	"thing things think thinking thinks third thirteen thirty this tho thomas thompson thorough " +
	"thoroughly those thou though thought thoughts thousand thousands thread threat threatened " +
	"threatening threats three threshold threw throat throne through throughout throw throwing thrown " +
	"thrust thus thy tide tie tied ties tight till timber time times timing tin tiny tip tired tissue " +
	"tissues title titles to tobacco today together tokyo told tolerance tom tomb tomorrow ton tone " +
	"tongue tonight tons too took tool tools top topic topics torn toronto total totally touch " +
	"touched touching tough tour toward towards tower town towns trace traced traces track tracks " +
	"tract trade trading tradition traditional traditionally traditions traffic tragedy trail train " +
	"trained training traits trans transaction transactions transfer transferred transformation " +
	"transformed transition translated translation transmission transmitted transport transportation " +
	"travel treasury treat treated treating treatment treaty tree trees tremendous trend trends trial " +
	"trials tribal tribe tribes tried tries trip triumph troops tropical trouble truck true truly " +
	"trunk trust trustees truth truths try trying tube tubes tumor tumors turkey turkish turn turned " +
	"turner turning turns tv twelve twentieth twenty twice two type types typical typically uk " +
	"ultimate ultimately un unable uncertain uncertainty uncle unconscious under underground " +
	"underlying understand understanding understood undertake undertaken undertaking undoubtedly " +
	"unemployment unexpected unfortunate unfortunately unhappy uniform union unions unique unit " +
	"united units unity universal universe universities university unknown unless unlike unlikely " +
	"unnecessary unpublished until unto unusual up upon upper upset upward urban urged urine us usa " +
	"usage use used useful useless user users uses using usual usually utility utmost utterly vacuum " +
	"vague vain valid validity valley valuable value valued values valve van variable variables " +
	"variance variation variations varied varies varieties variety various vary varying vascular vast " +
	"vector vegetable vegetables vegetation vehicle vehicles vein veins velocity venture verb verbal " +
	"verse verses version versions versus vertical very vessel vessels vi via vice victim victims " +
	"victor victoria victory video vienna vietnam view viewed views vigorous vii viii village " +
	"villages violation violence violent virgin virginia virtual virtually virtue virtues virus " +
	"visible vision visit visited visiting visitors visits visual vital vitamin viz vocabulary voice " +
	"voices void vol vols voltage volume volumes voluntary vote voted voters votes voting voyage wage " +
	"wages wait waited waiting wake wales walk walked walker walking wall walls walter want wanted " +
	"wanting wants war ward warfare warm warned warning warrant warren wars was wash washed " +
	"washington waste watch watched watching water waters wave waves way ways we weak weakness wealth " +
	"wealthy weapon weapons wear wearing weather web wedding week weekly weeks weight weights welcome " +
	"welfare well wells went were west western wet what whatever wheat wheel wheels when whence " +
	"whenever where whereas whereby wherein wherever whether which while whilst whispered white " +
	"whites who whole wholly whom whose why wide widely wider widespread widow width wife wild " +
	"wilderness will william williams willing wilson win wind window windows winds wine wing wings " +
	"winning winter wire wisconsin wisdom wise wish wished wishes wit with withdrawal within without " +
	"witness witnessed witnesses wives wolf woman women won wonder wondered wonderful wood wooden " +
	"woods wool word words wore work worked worker workers working works world worlds worn worried " +
	"worry worse worship worst worth worthy would wound wounded wright write writer writers writes " +
	"writing writings written wrong wrote xi xii xiii xiv yale yard yards ye year years yellow yes " +
	"yesterday yet yield yields york you young younger your yours yourself youth zealand zero zinc " +
	"zone zones"

var commonEnglishWords = func() map[string]struct{} {
	fields := strings.Fields(commonEnglishWordList)
	set := make(map[string]struct{}, len(fields))
	for _, w := range fields {
		set[w] = struct{}{}
	}
	return set
}()
