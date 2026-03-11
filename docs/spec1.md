# Technical requirements : use Go lang, Gorm ORM, gRPC (protoc), Kafka and Gin. You need to build 3 separate microservices for now:

Auth-service
User-service
API-Gateway-service

All services that are not API-Gateway use only Kafka and gRPC for communication between each other and API-Gateway, only API-Gateway uses HTTP/REST when talking with the client, requests go to API-Gateway, API-Gateway maps from HTTP/REST to gRPC, check if a request has a token and its valid (if that path needs a token), combines data from multiple services if needed, maps from proto to json and json to proto, microservices that are not API-Gateway do not need tokens, and do not check them. All services need to be ready to be deployed with docker for testing, database used is Postgres and is hosted remotely, all additional information will be included as environmental variables, include godotenv lib for testing with .env files. This needs to be professionally, follow practical standards and spec for the project, entites in project are an example and do not need to be followed. You are building just the backend and none of the frontend. Microservices are build independently but are in the same repo, there needs to be a shared folder contract where the types are stored for proto(server client and types) and kafka messages because they are shared across services.


# Uvod

Ovo je specifikacija projekta za generaciju **2025/26**. Specifikacioni tim ima zadatke:

1. Unapređenje postojeće specifikacije
2. Potencijalna proširenja i pisanje specifikacije za generaciju 2026/27.

Projekat obuhvata četiri funkcionalne celine i jednu mobilnu aplikaciju. Naš sistem simulira rad
banke sa dva glavna poslovna segmenta:
● **Osnovno bankarsko poslovanje** : upravljanje računima, različite transakcije, kursne liste,
krediti i platne kartice
● **Brokersko poslovanje** : trgovina hartijama od vrednosti (na berzi i OTC - over the
counter), kao i investicije u investicione fondove

Četiri glavne celine sistema su:

1. **Upravljanje korisnicima**
2. **Osnovno poslovanje banke** – računi, razl. transakcije, kursne liste, krediti i kartice
3. **Trgovina hartijama sa berze** – kupovina i prodaja, obračun poreza
4. **Proširenje trgovine hartijama** – OTC trgovina, investicioni fondovi, međubankarska
    komunikacija

Sistem podržava poslovanje u osam valuta _: RSD, EUR, CHF, USD, GBP, JPY, CAD i AUD._

## Učesnici u sistemu

Može se shvatiti da u sistemu postoje 3 “entiteta”:
● **Korisnici** : klijenti i zaposleni
● **Banka** :Firmasa sopstvenim računima u svim valutama - koriste se prilikom
konvertovanja novca, na te račune se uplaćuju provizije i odatle se isplaćuje kredit
klijentima. Ima investicije (hartije ili udeo u investicionim fondovima)
● **Država** :Firmakoja ima 1 račun na koji se uplaćuje porez

**Korisnici** se dele na **klijente** i **zaposlene**. U okviru svake od ovih grupa, uvedene su podgrupe
korisnika definisane kroz **role** , koje predstavljaju skup permisija potrebnih za rad unutar
sistema. Ove podgrupe su organizovane hijerarhijski po permisijama.

**Klijenti**
● **ClientBasic** - osnovne bankarske funkcije _(računi, transakcije, krediti, kartice, kursne
liste)_
● **ClientTrading** -plustrgovina hartijama sa berze, OTC trgovine i investiranja u fondove

**Zaposleni**
● **EmployeeBasic** – upravljanje klijentima i osnovnim bankarskim pojmovima (npr. otvaranje
računa, upravljanje karticama i kreditima)
● **EmployeeAgent** –plustrgovina hartijama sa berze, uz određene limite
● **EmployeeSupervisor** –plusbez limita; upravljanje OTC trgovinom, fondovima i agentima


```
● EmployeeAdmin –plusupravljanje svim zaposlenima
```
## Terminologija

### Osnovno poslovanje banke

**Tekući račun** _(engl. checking account)_ - za svakodnevne finansijske transakcije, uključujući
primanje i slanje novca, plaćanje računa, podizanje gotovine, itd. Može biti lični i poslovni.

**Devizni račun** _(engl. foreign currency account / FX account)_ - za primanje ili slanje novca u
stranoj valuti, obično za poslovne svrhe ili za putovanja u inostranstvo. Kod nas u sistemu,
devizni račun može imati novac samo u 1 valuti, dok u pravom životu to ne mora da bude
slučaj. Može biti lični i poslovni.

**Različite vrste transakcija**

1. Između računa različitih klijenata
    - plaćanja: _iste i različite valute_
2. Između računa istog klijenta
    - prenos: _iste valute_
    - transfer: _različite valute_

**Provizija** _(engl. fee) -_ novčana naknada koju banka uzima za svoje usluge. Kod nas se uzima
provizija prilikom konverzije novca. Provizija se prebacuje na račun banke.


### Trgovina hartijama sa berze

**Berza** _(engl. exchange)_ – javni sistem za trgovinu hartijama od vrednosti. Postoje primarno i
sekundarno tržište. Na _primarnom_ tržištu, izdavači hartija od vrednosti navode detalje o hartiji i
prodaju je zainteresovanim stranama, dok na _sekundarnom_ tržištu korisnici mogu kupovati i
prodavati već postojeće hartije.Naš sistem se bavi sekundarnim tržištem na berzi.

**Listing** – proces dodavanja hartije od vrednosti u sistem kako bi bila dostupna za trgovinu.

**Hartije od vrednosti** _(engl. securities)_ – finansijski instrumenti koji predstavljaju vlasništvo ili
pravo na isplatu. U sistemu su podržane sledeće vrste:

1. **Akcije** _(engl. stocks)_ – vlasnički udeo u kompaniji.
2. **Obveznice** _(engl. bonds)_ – dugovni instrumenti. Obavezu izdavača da plati nominalnu
    vrednost obveznice vlasniku na određeni datum u budućnosti, uz redovne kamatne
    isplate. _Mi se ovim ne bavimo._
3. **Valutni parovi** _(engl. forex pairs)_ – pravo na određenu količinu strane valute u zamenu za
    domaću valutu po trenutnom kursu.
4. **Terminski ugovori** _(engl. futures contracts)_ – obaveza kupovine/prodaje robe po unapred
    dogovorenoj ceni na dogovoreni datum u budućnosti. U našem sistemu će to biti fizička
    sredstva, npr. sirova nafta.
5. **Opcije** _(engl. options)_ – daje vlasniku pravo (ne i obaveza) kupovine/prodaje ugovorenog
    broja akcija po unapred definisanoj ceni u određenom periodu.

**Aktuari** (engl. actuaries) – zaposleni u banci zaduženi za berzanske transakcije. Dve vrste:

1. **Supervizori** – Odobravaju transakcije i nadgledaju tržište.
2. **Agenti** – Izvršavaju trgovinske naloge u ime klijenata i banke.

**Nalog za trgovinu** _(engl. orders)_ – zahtevi za kupovinu ili prodaju finansijskih instrumenata po
određenoj ceni.

**1. Tipovi naloga**
    a. _Market Order_ – trgovina po trenutno dostupnoj ceni.
    b. _Limit Order_ – kupovina/prodaja po zadatoj ceni ili boljoj.
    c. _Stop-Loss Order_ – automatska prodaja pri određenoj ceni radi smanjenja gubitaka.
    d. _Stop-Limit Order_ – kombinacija stop i limit naloga: kada cena dostigne stop nivo, kreira
       se limit order umesto market ordera.
**2. Načini izvršenja**
    a. _All or None (AON)_ – nalog koji se izvršava samo ako može biti u potpunosti
       realizovan, bez delimičnih transakcija.
    b. _Margin Order_ – nalog za trgovinu koristeći pozajmljena sredstva (margin trading), što
       omogućava veću kupovnu moć uz povećan rizik.

**Kapital i transakcije** – Korisnici mogu uložiti kapital stečen van berze i njime trgovati. Sistem
će omogućiti direktne transfer opcije između investicionih i tekućih računa.


### Proširenje trgovine hartijama od vrednosti

**OTC (Over-the-Counter) trgovina** - direktna kupoprodaju hartija od vrednosti između
učesnika, bez posredovanja berze. U našem sistemu, ovo se odnosi samo na kupoprodaju
akcija, principom kreiranja opcija (tj. opcionog ugovora) za te akcije.

**Investicioni fondovi** - finansijski instrumenti koji prikupljaju sredstva od više investitora i ta
sredstva ulažu u različite hartije od vrednosti, kao što su akcije, obveznice i drugi finansijski
proizvodi. Na taj način investitori zajednički ulažu i dele rizik i potencijalni profit, dok fondom
upravlja profesionalni menadžer, u našem sistemu to su supervizori.


# Upravljanje korisnicima

## Opis

Prva celina obuhvata _user management_ u projektu. Korisnici se dele na:

1. **Klijenti -** Oni su "stranke" u bankama. _Imaju tekući i/ili devizni račun i osnovne_
    _funkcionalnosti koje banka pruža (uplata, isplata i prebacivanje novca, plaćanje)._
2. **Zaposleni -** Oni su **obični zaposleni** ili **administratori** ( _kojima je dodeljena permisija_
    _administratora_ ). **Zaposlenima** upravljaju **administratori** preko portala (za upravljanje
    zaposlenih) dostupnog samo njima. _Oni će upravljati akcijama, ugovorima, osiguranjima_
    _itd._

Može se shvatiti da ovaj projekat ima dve aplikacije:
**Za Zaposlene:**
1.Portal za “Upravljanje zaposlenima”- _samo za administratore_

**Za Klijente:**
Ništa u ovoj cellini.

## Entiteti

**Zaposleni:
Podatak Tip podatka Primer Učestalost promena**
id Long 1234 Ne menja se
Ime String Petar Ne menja se
Prezime String Petrović Menja se (retko)
Datum rodjenja Date 1990-05-20 Ne menja se
Pol String M Menja se (retko)
Email adresa String petar@primer.raf Ne menja se
Broj telefona String +381645555555 Menja se (retko)
Adresa String Njegoševa 25 Menja se (retko)
Username String petar90 Ne menja se
Password String sifra1 * Menja se (retko)
SaltPassword String S4lt ** Ne menja se
Pozicija String Menadžer Menja se (retko)
Departman String Finansije Menja se (retko)
Aktivan Boolean true Menja se (retko)


^Aktivan zaposleni označava da li je zaposleni trenutno u aktivnom radnom odnosu. Zaposleni
imaju i listu permisija.

**Klijent
Podatak Tip podatka Primer Učestalost promena**

```
id Long 1234 Ne menja se
Ime String Petar Ne menja se
Prezime String Petrović Menja se (retko)
Datum rodjenja Long (Unix vreme) Ne menja se
Pol String M Menja se (retko)
Email adresa String petar@primer.raf Ne menja se
Broj telefona String +381645555555 Menja se (retko)
Adresa String Njegoševa 25 Menja se (retko)
Password String sifra1 * Menja se (retko)
SaltPassword String S4lt ** Ne menja se
Povezani racuni List/ Array [racun_1, racun_2, racun_3, ...] Menja se (povremeno)
```
* **Password** - hashed (and salted po zelji) password
** **SaltPassword** - dodatno polje ukoliko se koristi “ _Salt and Hash_ ” pristup heširanja. Generiše
se (random) pri pravljenju jednog korisnika i on je unikatan za svaku instancu korisnika.
_* Napomena: kreiranje Klijenta je u sklopu druge celine. Zaposleni kreira Klijenta prilikom
kreiranja računa_

## Portal za upravljanje Zaposlenima

Mogu da pristupe samo zaposleni koji su administratori. Imaju pregled svih zaposlenih (admin i
običnog zaposlenog), ali mogu da edituju samo običnog zaposlenog.

### Lista svih zaposlenih

**Prikaz:** Administrator vidi sve zaposlene, prikazani su _ime i prezime, email, pozicija, broj
telefona i da li je aktivan ili ne._
**Filtriranje:** Moguće je filtriranje po email-u, imenu i prezimenu, kao i poziciji.
**Klikom na običnog zaposlenog** otvara se novi prozor gde administrator može da izmeni sve
informacija osim ID-a i passworda. **Napomena** : može da deaktivira radnika i upravlja
permisijama (može i da dodeli admin permisiju).

*** Permisije:** Svaki zaposleni ima set permisija odnosno šta mu je dozvoljeno da radi na sistemu
(npr. trguje akcijama, samo gleda akcije, sklapa ugovore i nova osiguranja...).


### Kreiranje i aktivacija naloga Zaposlenog

Samo **administrator** može da kreira nalog zaposlenog.

Administrator unosi sva polja osim password-a. Po default-u se podrazumeva da je korisnik
aktivan, ali moguće je napraviti i korisnika koji nije aktivan. Nakon što **administrator kreira
zaposlenog** , potrebno je omogućiti zaposlenom da može da **aktivira nalog** i unese svoju
lozinku. To na primer može biti realizovano tako što se korisniku pošalje mejl sa linkom koji
otvara formu za unos lozinke (2 polja radi potvrde lozinke). Nakon toga dobija confirmation mail.

*** Unikatnost naloga:** u bazi ne smeju biti 2 naloga sa istim emailom ( _email je unique_ )
*** Password constraints:** najmanje 8, a najviše 32 karaktera, sa najmanje 2 broja, 1 velikim i 1
malim slovom.

## Login stranica

Na login stranici postoje 1 funkcionalnosti - login.

### Login - autentifikacija korisnika

Korisnik se loguje u sistem unosom email-a i password-a. Korisnik takođe ima opciju da
resetuje svoju lozinku ako ju je zaboravio - ovo se takođe npr. može uraditi preko email-a sa
linkom ka stranici za resetovanje lozinke. Dodatno, zbog sigurnosti, ukoliko korisnik zatvori svoj
web pretraživač potrebno je tražiti da se korisnik ponovno uloguje.

## Autentifikacija i autorizacija korisnika

Autentifikacija korisnika je gore opisana u okviru logina na _Login stranici._

**Autorizacija mora biti implementirana preko access/refresh tokena.** Autorizacija korisnika
se odnosi na to da za izvršenje većine zahteva/operacija, potrebno je proveriti da li korisnik ima
permisiju da izvrši tu operaciju. Ukoliko nema, vraća se odgovarajuća greška.
_* U opštem slučaju, korisnici ne bi trebalo da su svesni o postojanju operacija za koje nemaju
dozvolu._


