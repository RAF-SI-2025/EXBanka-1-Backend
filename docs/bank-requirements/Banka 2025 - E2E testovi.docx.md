# Banka 2025 \- end to end testovi

[**Uvod	2**](#uvod)

[**Upravljanje korisnicima	2**](#upravljanje-korisnicima)

[Feature: Autentifikacija korisnika	2](#feature:-autentifikacija-korisnika)

[Feature: Kreiranje i upravljanje zaposlenima	2](#feature:-kreiranje-i-upravljanje-zaposlenima)

[**Osnovno poslovanje banke	4**](#osnovno-poslovanje-banke)

[Feature: Kreiranje računa klijenta	4](#feature:-kreiranje-računa-klijenta)

[Feature: Plaćanja i transferi	4](#feature:-plaćanja-i-transferi)

[Feature: Upravljanje karticama	5](#feature:-upravljanje-karticama)

[Feature: krediti	5](#feature:-krediti)

[**Trgovina hartijama sa berze	6**](#trgovina-hartijama-sa-berze)

[Feature: Validacija pravila za postavljanje naloga i graničnih slučajeva	6](#feature:-validacija-pravila-za-postavljanje-naloga-i-graničnih-slučajeva)

[Feature: Kupovina i prodaja akcija i forex parova	7](#feature:-kupovina-i-prodaja-akcija-i-forex-parova)

[Feature: Obračun poreza na osnovu tipa sredstva i profita	8](#feature:-obračun-poreza-na-osnovu-tipa-sredstva-i-profita)

[Feature: Prikaz i praćenje performansi portfolija	9](#feature:-prikaz-i-praćenje-performansi-portfolija)

[**Proširenje trgovine hartijama	10**](#proširenje-trgovine-hartijama)

[Feature: Obrada OTC trgovine korišćenjem SAGA mehanizma	10](#feature:-obrada-otc-trgovine-korišćenjem-saga-mehanizma)

[Feature: Upravljanje uplatama i isplatama iz fondova	11](#feature:-upravljanje-uplatama-i-isplatama-iz-fondova)

[Feature: Međubankarski prenosi i integritet transakcija	11](#feature:-međubankarski-prenosi-i-integritet-transakcija)

## 

## 

## Uvod {#uvod}

Ovo su end to end testovi koji su pisani za frontend. Specifikacija 2025/26 ima zadatke:  
1\. Napiše još testova za ostale feature  
2\. Za date i nove testove napiše ih iz ugla backend-a

## Upravljanje korisnicima {#upravljanje-korisnicima}

### Feature: Autentifikacija korisnika {#feature:-autentifikacija-korisnika}

**Scenario**: Uspešno logovanje zaposlenog  
    **Given** Zaposleni se nalazi na login stranici  
    **When** Unese validan email "petar@primer.raf" i lozinku "Sifra123"  
    **Then** Sistem ga preusmerava na početnu stranicu

  **Scenario**: Neuspešno logovanje zbog pogrešne lozinke  
    **Given** Zaposleni se nalazi na login stranici  
    **When** Unese validan email "petar@primer.raf" i pogrešnu lozinku "pogresnaSifra"  
    **Then** Sistem prikazuje poruku "Neispravni kredencijali"  
    **And** Logovanje se onemogućava nakon 3 neuspešna pokušaja

  **Scenario**: Reset lozinke putem email-a  
    **Given** Zaposleni se nalazi na login stranici  
    **When** Klikne na opciju "Zaboravljena lozinka"  
    **And** Unese svoj email "petar@primer.raf"  
    **Then** Sistem mu šalje email sa linkom za reset lozinke  
    **And** Link ističe nakon 15 minuta

### **Feature**: Kreiranje i upravljanje zaposlenima {#feature:-kreiranje-i-upravljanje-zaposlenima}

  **Scenario**: Administrator kreira novog zaposlenog  
    **Given** Administrator je ulogovan i na stranici za kreiranje zaposlenog  
    **When** Popuni sva polja osim lozinke i potvrdi unos  
    **Then** Novi zaposleni dobija email za aktivaciju naloga  
    **And** Link za aktivaciju ističe nakon 24h

  **Scenario**: Administrator menja podatke zaposlenog  
    **Given** Administrator je na stranici za upravljanje zaposlenima  
    **When** Klikne na "Izmeni" za zaposlenog "Marko Marković"  
    **And** Promeni broj telefona na "+381645555556"  
    **And** Promeni departman na "IT"  
    **Then** Podaci zaposlenog su ažurirani  
    **And** Zaposleni dobija email obaveštenje o promenama

  **Scenario**: Administrator deaktivira zaposlenog  
    **Given** Administrator je na stranici za upravljanje zaposlenima  
    **When** Klikne na dugme "Deaktiviraj" za zaposlenog "Marko Marković"  
    **Then** Zaposleni više nema pristup sistemu  
    **And** Sve njegove aktivne sesije se automatski odjavljuju

## 

## Osnovno poslovanje banke {#osnovno-poslovanje-banke}

### **Feature**: Kreiranje računa klijenta {#feature:-kreiranje-računa-klijenta}

  **Scenario**: Kreiranje tekućeg računa za klijenta  
    **Given** Zaposleni je ulogovan i na stranici za kreiranje računa  
    **When** Unese podatke klijenta i izabere "Tekući račun"  
    **And** Unese početno stanje "10000 RSD"  
    **Then** Sistem kreira račun i klijent dobija email obaveštenje  
    **And** Račun je odmah vidljiv u njegovom profilu  
    **And** Proverava se da li je automatski kreirana kartica ako je opcija bila selektovana

  **Scenario**: Kreiranje deviznog računa sa početnim stanjem  
    **Given** Zaposleni je ulogovan i na stranici za kreiranje računa  
    **When** Unese podatke klijenta i izabere "Devizni račun"  
    **And** Unese početno stanje "500 EUR"  
    **Then** Sistem kreira devizni račun  
    **And** Račun se prikazuje u listi klijentovih računa  
    **And** Proverava se da li je automatski kreirana kartica ako je opcija bila selektovana

### **Feature**: Plaćanja i transferi {#feature:-plaćanja-i-transferi}

  **Scenario**: Klijent uspešno izvršava plaćanje drugom klijentu  
    **Given** Klijent je ulogovan i na stranici "Novo plaćanje"  
    **When** Unese broj računa primaoca "265000000000123456"  
    **And** Unese iznos "5000 RSD"  
    **And** Klikne na dugme "Potvrdi"  
    **Then** Transakcija se uspešno izvršava  
    **And** Klijent dobija email potvrdu  
    **And** Plaćanje se vidi u istoriji transakcija  
    **And** Vidi se ažurirano stanje računa primaoca

  **Scenario**: Neuspešno plaćanje zbog nedovoljnih sredstava  
    **Given** Klijent je ulogovan i na stranici "Novo plaćanje"  
    **When** Unese broj računa primaoca "265000000000123456"  
    **And** Unese iznos "500000 RSD"  
    **And** Klikne na dugme "Potvrdi"  
    **Then** Sistem prikazuje poruku "Nedovoljno sredstava na računu"  
    **And** Plaćanje se ne izvršava  
    **And** Vidi se da je stanje računa primaoca ne promenjeno

### **Feature**: Upravljanje karticama {#feature:-upravljanje-karticama}

  **Scenario**: Klijent blokira svoju karticu  
    **Given** Klijent je na stranici "Moje kartice"  
    **When** Klikne na dugme "Blokiraj" za karticu "\*\*\*\* 5571"  
    **Then** Kartica se označava kao blokirana  
    **And** Klijent dobija email obaveštenje

  **Scenario**: Zaposleni odblokira karticu klijenta  
    **Given** Zaposleni je ulogovan i na stranici za upravljanje karticama  
    **When** Pronađe karticu "\*\*\*\* 5571"  
    **And** Klikne na dugme "Odblokiraj"  
    **Then** Kartica se ponovo može koristiti  
    **And** Klijent dobija email obaveštenje o odblokiranju

  **Scenario**: Klijent pokušava da aktivira deaktiviranu karticu  
    **Given** Klijent je na stranici "Moje kartice"  
    **When** Pokuša da aktivira karticu koja je deaktivirana  
    **Then** Sistem ne dozvoljava aktivaciju  
    **And** Prikazuje poruku "Kartica je deaktivirana i ne može se ponovo aktivirati"

### Feature: krediti {#feature:-krediti}

**Scenario**: Klijent podnosi zahtev za kredit  
    **Given** Klijent je ulogovan i na stranici "Podnošenje zahteva za kredit"  
    **When** Unese validne podatke za kredit od "10000 RSD" sa rokom otplate "24 meseca"  
    **And** Klikne na dugme "Podnesi zahtev"  
    **Then** Sistem beleži zahtev  
    **And** Klijent dobija email potvrdu

  **Scenario**: Zaposleni odobrava kredit  
    **Given** Zaposleni je ulogovan i na stranici "Zahtevi za kredit"  
    **When** Pregleda zahtev klijenta "Petar Petrović" za kredit "10000 RSD"  
    **And** Klikne na dugme "Odobri"  
    **Then** Sistem označava kredit kao odobren  
    **And** Klijent dobija email obaveštenje o odobrenju kredita  
    **And** Iznos kredita se dodaje na klijentov račun

## Trgovina hartijama sa berze {#trgovina-hartijama-sa-berze}

### **Feature:** Validacija pravila za postavljanje naloga i graničnih slučajeva {#feature:-validacija-pravila-za-postavljanje-naloga-i-graničnih-slučajeva}

**Scenario:** Nalog agenta zahteva odobrenje zbog prekoračenja dnevnog limita  
  **Given** da sam prijavljen kao Agent  
  **And** moj iskorišćeni limit danas iznosi 95.000 RSD  
  **When** postavim Limit nalog u vrednosti od 10.000 RSD  
  **Then** nalog treba da zahteva odobrenje  
  **And** status treba da bude "Na čekanju"

**Scenario:** Nalog odbijen van radnog vremena berze  
  **Given** da postavljam Market nalog  
  **And** berza je trenutno zatvorena  
  **When** pošaljem nalog  
  **Then** sistem treba da odbije nalog  
  **And** prikaže poruku "Berza je zatvorena"

**Scenario:** Automatsko odbijanje ugovora o budućnosti sa isteklim datumom  
  **Given** da sam izabrao ugovor o budućnosti sa prošlim datumom poravnanja  
  **When** pokušam da postavim BUY nalog  
  **Then** nalog treba da bude odbijen  
  **And** razlog odbijanja treba da bude zabeležen

**Scenario:** Stop-Limit nalog kreira Limit nalog kada se dostigne stop cena  
  **Given** da postavim Stop-Limit BUY nalog sa stop \= 100 i limit \= 98  
  **When** tržišna cena dostigne 100  
  **Then** treba da se kreira Limit BUY nalog na 98

**Scenario:** All-or-None zastavica blokira delimičnu realizaciju  
  **Given** da postavim Market SELL nalog za 1.000 jedinica  
  **And** na tržištu je dostupno samo 700 jedinica  
  **And** opcija All-or-None je postavljena na true  
  **When** pošaljem nalog  
  **Then** nalog ne treba da se izvrši  
  **And** njegov status treba da ostane "Na čekanju"

**Scenario:** Postavljanje i izvršenje običnog market naloga tokom radnog vremena berze  
  **Given** da sam prijavljen i da je berza otvorena  
  **When** postavim Market BUY nalog za 5 akcija AAPL  
  **Then** nalog treba odmah da se izvrši po trenutnoj ask ceni  
  **And** moje stanje treba da se ažurira u skladu s tim

### **Feature:** Kupovina i prodaja akcija i forex parova {#feature:-kupovina-i-prodaja-akcija-i-forex-parova}

**Scenario:** Klijent kupuje akcije po tržišnoj ceni  
  **Given** da imam 10.000 RSD na svom investicionom računu  
  **And** trenutna Ask cena za AAPL je 1.000 RSD  
  **When** postavim Market BUY nalog za 5 jedinica  
  **Then** ukupna cena treba da uključuje cenu akcija i proviziju  
  **And** treba da posedujem 5 AAPL akcija

**Scenario:** Klijent prodaje forex po trenutnoj bid ceni  
  **Given** da posedujem 1.000 jedinica EUR/USD  
  **And** Bid cena je 1.1200  
  **When** postavim Market SELL nalog za 500 jedinica  
  **Then** treba da dobijem 560 USD  
  **And** moje vlasništvo treba da se smanji za 500 jedinica

**Scenario Outline:** Limit BUY nalog se izvršava samo po povoljnoj ceni  
  **Given** da postavim Limit BUY nalog za \<symbol\> po ceni \<targetPrice\>  
  **And** trenutna Ask cena je \<currentAsk\>  
  **When** Ask cena dostigne \<newAsk\>  
  **Then** nalog treba da se izvrši

**Examples:**

| symbol | targetPrice  | currentAsk  | newAsk  |
| :---- | :---- | :---- | :---- |
| USD/JPY | 109.500 | 110.200 | 109.400 |
| GBP/EUR | 1.1700 | 1.1746 | 1.1695 |

**Scenario:** Kupovina forex-a uz konverziju iz neosnovne valute  
  **Given** da imam 10.000 RSD na svom investicionom računu  
  **And** želim da kupim EUR/USD  
  **When** pokrenem kupovinu  
  **Then** sistem treba da konvertuje RSD u USD koristeći trenutni kurs  
  **And** izvrši kupovinu sa konvertovanim iznosom

**Scenario:** Sistem sprečava forex nalog ispod minimalne veličine lota  
  **Given** da pokušam da kupim 3 jedinice GBP/USD  
  **And** minimalna veličina lota je 10  
  **When** pošaljem nalog  
  **Then** sistem treba da ga odbije  
  **And** prikaže "Nalog ispod minimalne veličine lota"

**Scenario:** Prikaz uživo grafikona za izabranu akciju ili valutni par  
  **Given** da izaberem TSLA akciju sa liste hartija  
  **When** kliknem na "Prikaži grafikon"  
  **Then** treba da vidim uživo grafikon kretanja cene u poslednja 24 časa

### **Feature:** Obračun poreza na osnovu tipa sredstva i profita {#feature:-obračun-poreza-na-osnovu-tipa-sredstva-i-profita}

**Scenario:** Izračunavanje poreza za više tipova sredstava  
  **Given** da imam profit od akcija, opcija i fjučersa  
  **When** otvorim svoj poreski izveštaj  
  **Then** sistem treba da:

* primeni ispravne stope po tipu sredstva  
* prikaže ukupnu poresku obavezu  
* omogući izvoz u PDF

**Scenario:** Otkrivanje neslaganja u profitu i označavanje transakcije  
  **Given** da je prijavljeni profit 0 RSD  
  **And** sistemsko izračunavanje pokazuje dobit od 5.000 RSD  
  **When** se pokrene poreska provera  
  **Then** transakcija treba da bude označena  
  **And** obeležena za ručno usklađivanje

**Scenario:** Prilagođavanje poreske obaveze nakon izmene  
  **Given** da ispravim nabavnu cenu transakcije  
  **When** poreski modul ponovo izračuna dobit  
  **Then** nova poreska obaveza treba da se ažurira u skladu sa tim

**Scenario:** Pregled poreskog izveštaja za prethodnu fiskalnu godinu  
  **Given** da sam klijent sa trgovanjem u 2024\. godini  
  **When** otvorim "Poreske izveštaje" i filtriram po godini 2024  
  **Then** treba da vidim sve oporezive događaje i obaveze za tu godinu

### 

### **Feature:** Prikaz i praćenje performansi portfolija {#feature:-prikaz-i-praćenje-performansi-portfolija}

**Scenario:** Mesečni prikaz aktuarskog profita  
  **Given** da filtriram aktuarski panel za "Poslednja 3 meseca"  
  **Then** treba da vidim:

| Tip | Mesec | Vrednost |
| ----- | ----- | ----- |
| Ostvareni profit | Jan | ... |
| Neostvareni | Jan | ... |

**Scenario:** Izbegavanje dupliranog profita iz zajedničkih sredstava  
  **Given** da AAPL akcije postoje u dva fonda  
  **When** se računa ukupni profit  
  **Then** dobit treba da se izračuna posebno po fondu  
  **And** ne sme biti duplog brojanja

**Scenario:** Preračunavanje pozicija nakon delimičnog povlačenja  
  **Given** da povučem 25% svoje pozicije u fondu  
  **When** povlačenje bude završeno  
  **Then** sistem treba da ažurira moj procenat vlasništva i trenutnu vrednost

**Scenario:** Portfolio odražava najnoviji NAV nakon osvežavanja  
  **Given** da imam jedinice u fondu  
  **When** neto vrednost aktive fonda se promeni  
  **Then** moj portfolio treba da odražava ažuriranu vrednost nakon osvežavanja

**Scenario:** Klijent vidi ceo portfolio sa rezime statistikom  
  **Given** da sam prijavljen kao klijent  
  **When** otvorim "Moj Portfolio"  
  **Then** treba da vidim:

* Ukupnu vrednost računa  
* Ukupni profit  
* Raspodelu između fondova i direktnih ulaganja

## Proširenje trgovine hartijama {#proširenje-trgovine-hartijama}

### **Feature:** Obrada OTC trgovine korišćenjem SAGA mehanizma {#feature:-obrada-otc-trgovine-korišćenjem-saga-mehanizma}

**Scenario:** Neuspešan prenos vlasništva pokreće poništavanje  
 **Given** da je OTC trgovina u toku  
 **And** da su sredstva i akcije rezervisani  
 **When** prenos vlasništva ne uspe  
 **Then** sistem treba da refundira kupca  
 **And** da vrati vlasništvo nad akcijama prodavcu  
 **And** da označi transakciju kao "Poništena"

**Scenario:** Ponovni pokušaj povraćaja sredstava u slučaju greške  
 **Given** da povraćaj sredstava ne uspe zbog mrežne greške  
 **When** sistem pokuša ponovo da izvrši povraćaj  
 **Then** treba da pokuša najviše 3 puta  
 **And** da obavesti administratore ako svi pokušaji ne uspeju

**Scenario:** Sprečavanje duplog rezervisanja akcija u istovremenim OTC pregovorima  
 **Given** da prodavac započne dve OTC trgovine sa istim akcijama  
 **When** jedna od trgovina bude finalizovana i akcije rezervisane  
 **Then** druga trgovina treba da se zaključa  
 **And** da prikaže: "Nedovoljno zaliha za ovu ponudu"

**Scenario:** OTC trgovina ne uspe zbog nedovoljnih sredstava tokom izvršenja  
 **Given** da kupac ima rezervisana sredstva za OTC trgovinu  
 **And** da druga transakcija potroši ta sredstva pre izvršenja  
 **When** OTC trgovina pokuša finalni prenos  
 **Then** sistem treba da poništi trgovinu i započne povraćaj

**Scenario:** Uspešna realizacija OTC trgovine  
 **Given** da su kupac i prodavac postigli dogovor o ceni, količini i datumu poravnanja  
 **When** kupac odluči da iskoristi opciju pre isteka roka  
 **Then** sistem treba da:

* Prenese sredstva prodavcu  
* Prenese vlasništvo kupcu  
* Označi ugovor kao izvršen

### 

### **Feature:** Upravljanje uplatama i isplatama iz fondova {#feature:-upravljanje-uplatama-i-isplatama-iz-fondova}

**Scenario:** Delimična likvidacija sredstava zbog nedovoljne likvidnosti  
 **Given** da zahtevam isplatu 50.000 RSD  
 **And** da fond ima samo 20.000 RSD likvidnih sredstava  
 **When** isplata se obradi  
 **Then** sistem treba da proda sredstva kako bi pokrio preostalih 30.000 RSD  
 **And** da me obavesti o odloženoj isplati

**Scenario:** Preračunavanje pozicije u fondu nakon promene vrednosti imovine  
 **Given** da posedujem 10% fonda  
 **And** da imovina u fondu izgubi 20% vrednosti  
 **When** osvežim portfolio  
 **Then** vrednost moje pozicije i profit treba da budu ažurirani

**Scenario:** Blokirati uplatu ako je isplata na čekanju  
 **Given** da imam aktivan zahtev za isplatu iz fonda  
 **When** pokušam da uplatim u isti fond  
 **Then** sistem treba da blokira uplatu

**Scenario:** Odbijanje uplate ispod minimalnog iznosa  
 **Given** da je minimalna uplata u fond 1.000 RSD  
 **When** pokušam da uplatim 900 RSD  
 **Then** sistem treba da odbije transakciju

**Scenario:** Supervizor uplaćuje u fond u ime banke  
 **Given** da sam prijavljen kao Supervizor  
 **When** izaberem bankovni račun i uplatim 100.000 RSD u fond  
 **Then** stanje fonda treba da se poveća za taj iznos

**Scenario:** Klijent ulaže u aktivni fond  
 **Given** da sam klijent  
 **And** da imam dovoljno sredstava  
 **When** izaberem fond i investiram 2.000 RSD  
 **Then** uplata treba da bude uspešna  
 **And** moj portfolio treba da se ažurira

### **Feature:** Međubankarski prenosi i integritet transakcija {#feature:-međubankarski-prenosi-i-integritet-transakcija}

**Scenario:** Otkazivanje uplate ako je banka primalac neodgovarajuća  
 **Given** da pokrenem uplatu iz Banke A ka Banci B  
 **And** da Banka B ne odgovara 10 sekundi  
 **When** istekne vremenski rok  
 **Then** uplata treba da bude otkazana  
 **And** sredstva refundirana pošiljaocu

**Scenario:** Evidentirati kompletan audit trag međubankarske transakcije  
 **Given** da je uplata između Banke A i Banke B uspešno završena  
 **When** pristupim istoriji transakcija  
 **Then** log treba da sadrži:

| Polje | Vrednost |
| ----- | ----- |
| Banka Pošiljalac | Banka A |
| Banka Primalac | Banka B |
| Vreme Slanja | \<vremenska oznaka\> |
| Vreme Prijema | \<vremenska oznaka\> |
| Status | Uspešno |

**Scenario:** Odbijanje prenosa kada pošiljalac nema dovoljno sredstava  
 **Given** da imam 100 RSD na računu  
 **When** pokušam da prebacim 200 RSD na drugu banku  
 **Then** sistem treba da odbije transakciju  
 **And** da prikaže: "Nedovoljno sredstava"

**Scenario:** Uspešan prenos sredstava između banaka  
 **Given** da imam 10.000 RSD na računu  
 **And** da pokrenem prenos ka klijentu u drugoj banci  
 **When** prenos bude završen  
 **Then** račun primaoca treba da se poveća za 10.000 RSD

