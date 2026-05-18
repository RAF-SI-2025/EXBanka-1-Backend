# Komunikacija izmedju banaka

[Komunikacija između banaka	2](#komunikacija-između-banaka)

[Plaćanja	2](#plaćanja)

[Primer	3](#primer)

[OTC Trgovina	4](#otc-trgovina)

[Dobavljanje OTC ponuda druge banke	4](#dobavljanje-otc-ponuda-druge-banke)

[Pregovaranje	4](#pregovaranje)

[**Primer 1 \- pregovaranje**	4](#primer-1---pregovaranje)

[**Primer 2 \- postignut dogovor**	5](#primer-2---postignut-dogovor)

[Izvršavanje kupoprodaje	5](#izvršavanje-kupoprodaje)

[**Primer 1 \- iskorišćavanje opcionog ugovora**	7](#primer-1---iskorišćavanje-opcionog-ugovora)

[**Primer 2 \- ne iskorišćavanje opcionog ugovora**	8](#primer-2---ne-iskorišćavanje-opcionog-ugovora)

## Komunikacija između banaka {#komunikacija-između-banaka}

Nije nužno da sve 4 banke komuniciraju međusobno, dovoljno je da svaka banka komunicira sa jednom drugom.

**Protokol za komunikaciju \- generacija 2024/25:** https://arsen.srht.site/si-tx-proto/

### Plaćanja {#plaćanja}

Plaćanje se izvršava ili u celosti, ili ne uopšte.

**Učesnici:**  
\- Banka Pošiljaoca (Banka A): Banka klijenta koji šalje novac.  
\- Banka Primaoca (Banka B): Banka klijenta koji prima novac.

**Flow:**

1) **Inicijalizacija Transakcije (Banka A):**  
   1) Klijent inicira plaćanje  
   2) Banka A identifikuje banku primaoca (Banka B) na osnovu prve tri cifre broja računa primaoca.  
2) **Provera Stanja i Rezervacija Sredstava (Banka A):**  
   1) Banka A proverava da li pošiljalac ima dovoljno raspoloživih sredstava (availableBalance \= sredstva \- rezervisanaSredstva).  
   2) Ako je *availableBalance* dovoljan, Banka A rezerviše potrebna sredstva na računu pošiljaoca.  
3) **Priprema Transakcije (Faza 1 2-Phase Commit):**  
   1) Banka A šalje "Prepare" zahtev Banci B.  
   2) Zahtev sadrži:  
      1) Početna vrednost (valuta, iznos): Originalni iznos i valuta transakcije.  
      2) Pošiljalac: Identifikacija pošiljaoca.  
      3) Primalac: Identifikacija primaoca.  
   3) Banka A čeka na odgovor od Banke B.  
4) **Provera Spremnosti (Banka B):**  
   1) Banka B prima "Prepare" zahtev od Banke A.  
   2) Banka B proverava da li može da obradi transakciju (npr. da li primalac postoji, da li je račun aktivan, itd.).  
   3) Banka B izračunava kurs i proviziju koji će se primeniti na transakciju.  
   4) Banka B šalje odgovor Banci A:  
      1) Ready: Ako je Banka B spremna da obradi transakciju. Odgovor sadrži:  
         1) Početna vrednost (valuta, iznos): Originalni iznos i valuta transakcije.  
         2) Krajnja vrednost (valuta, iznos): Iznos i valuta koji će biti uplaćeni na račun primaoca nakon konverzije i provizije.  
         3) Pošiljalac: Identifikacija pošiljaoca.  
         4) Primalac: Identifikacija primaoca.  
         5) Kurs: Kurs koji se koristi za konverziju.  
         6) Provizija: Iznos provizije.  
      2) Not Ready: Ako Banka B nije spremna da obradi transakciju. Odgovor može da sadrži razlog zašto transakcija ne može da se obradi.  
5) **Obrada Rezultata (Banka A):**  
   1) Ako Banka A primi Not Ready odgovor od Banke B:  
      1) Banka A prekida transakciju.  
      2) Banka A oslobađa rezervisana sredstva na računu pošiljaoca.  
      3) Banka A obaveštava pošiljaoca da transakcija nije uspela.  
   2) Ako Banka A primi Ready odgovor od Banke B:   
      1) Banka A nastavlja sa sledećim korakom.  
6) **Commit Transakcije (Faza 2 2-Phase Commit):**  
   1) Banka A šalje "Commit" zahtev Banci B.  
   2) Zahtev sadrži sve podatke iz "Ready" odgovora.  
   3) Banka A skida rezervisana sredstva sa računa pošiljaoca.  
7) **Dodavanje Sredstava Primaocu (Banka B):**  
   1) Banka B prima "Commit" zahtev od Banke A.  
   2) Banka B dodaje iznos (Krajnja vrednost) na račun primaoca.  
   3) Banka B šalje potvrdu Banci A da je transakcija uspešno izvršena.  
8) **Finalizacija (Banka A):**  
   1) Banka A prima potvrdu od Banke B.  
   2) Banka A obaveštava pošiljaoca da je transakcija uspešno izvršena.

**Scenario neuspeha**  
Ako bilo koji korak u procesu ne uspe, transakcija se prekida, a sve promene se poništavaju.

**Napomena:** U slučaju prekida, banke moraju da imaju mehanizme za oslobađanje rezervisanih sredstava i vraćanje sistema u prethodno stanje. Potrebno je definisati format poruka koje se razmenjuju između banaka (npr. JSON, XML). Treba definisati mehanizme za rešavanje grešaka i ponavljanje transakcija u slučaju privremenih problema u komunikaciji.

#### Primer {#primer}

1) Otvaranje aplikacije i prijava  
2) Opcija “Novo plaćanje”  
3) Podaci o transakciji:  
   1) Primalac: **"Nikola Petrović"**  
   2) Broj računa primaoca: **4441234567890**  
   3) Iznos: **150**  
   4) Valuta: **EUR**  
4) Dugme "Pošalji"  
5) Aplikacija prikazuje poruku: *Transakcija u obradi…*  
6) Čekanje potvrde  
   1) Notifikacija o uspehu: *Transakcija uspešno završena\! Iznos 150 EUR prebačen na račun 441234567890\.*  
   2) Notifikacija o neuspehu: *Transakcija nije uspela\! Račun primaoca je neaktivan*

### OTC Trgovina {#otc-trgovina}

Komuniciraju 2 klijenta ili 2 supervizora. 

#### Dobavljanje OTC ponuda druge banke {#dobavljanje-otc-ponuda-druge-banke}

Klijenti vide ponude Kllijenata, Aktuari vide ponude Aktuara.  
Ovo može da se vrši na nekom vremenskom intervalu ili kada neko uđe na stranicu \- pošaljemo zahtev za najnovije OTC ponude. Izgled je kao u [Portalu: OTC Trading](#heading=h.qdaiis2093wi) *(može se dodatno prikazati banka prodavca).*

#### Pregovaranje {#pregovaranje}

Kupac može da napravi ponudu za željene akcije gde specifizira *količinu akcija, cenu po akciji (u valuti u kojoj je cena te akcije), settlementDate i premium (cena opcionog ugovora).* Nakon što se da inicijalna ponuda, otvara se stranica Aktivne ponude. Svaka komunikacija može biti jedna od sledećih akcija:  
1\. Prihvatanje ponude  
2\. Odustajanje od ponude \- briše se ponuda i kupcu i prodavcu  
3\. Slanje kontraponude \- *količina akcija, cena po akciji (u valuti cene te akcije), settlementDate i premium*

##### **Primer 1 \- pregovaranje** {#primer-1---pregovaranje}

Situacija:   
Pregovaranje o opciji za 100 akcija AAPL \- price per stock unit is $220.

Flow:

1. Prodavac ulazi na Portal: Moj Portfolio

   1.  Selektuje 100 akcija AAPL i prebacuje ih na javni režim  
   2. Ove akcije su uspesno objavljene i vidljive u OTC Portalu.  
2. Kupac ulazi na [Portal: OTC](#heading=h.qdaiis2093wi)  
   * Vidi ponudu iz druge banke  
   * Klikne na "Napravi ponudu" i predlaže:  
     * Cena po akciji: $180 USD   
     * Količina: 50 akcija  
     * Settlement date: 05.04.2025.  
     * Premium: $700  
   * Klikne na "Pošalji ponudu"  
2. Prodavac vidi obavestenje i ulazi na [Aktivne ponude](#heading=h.2qkwzeblaku7)  
   * Vidi koja ponuda je izmenjena  
   * Klik na “Predlozi kontra ponudu”  
     * Cena po akciji: $200 USD *\- promenjeno*  
     * Količina: 50 akcija  
     * Settlement date: 05.04.2025.  
     * Premium: $1150 *\- promenjeno*  
   * Klik na “Pošalji kontra ponudu”  
3. Kupac vidi obavestenje i ulazi na [Aktivne ponude](#heading=h.2qkwzeblaku7)  
   * Vidi koja ponuda je izmenjena  
   * Klik na “Prihvati ponudu”

##### **Primer 2 \- postignut dogovor** {#primer-2---postignut-dogovor}

1\. Kupac automatski **plaća premium ocpionog ugovora** sa svog računa.   
2\. Prodavac **ne može prodati tih 50 akcija** dok ugovor ne istekne ili Kupac ne iskoristi opciju.  
3\. Kupac sada vidi opcioni ugovor u stranici [Sklopljeni ugovori](#heading=h.odfmwkduot6z).

#### 

#### Izvršavanje kupoprodaje {#izvršavanje-kupoprodaje}

Kupoprodaja se izvršava po SAGA patternu. 

**Učesnici:**  
\- Banka Pošiljaoca (Banka A): Banka klijenta koji šalje iskorišćava opcioni ugovor  
\- Banka Primaoca (Banka B): Banka klijenta koji poseduje akcije.

**Flow:** 

1)  **Rezervacija sredstava kupca (novac) – Banka A**  
   1) **Transakcija:**  
      1) Banka A proverava availableBalance kupca *(sredstva \- rezervisanaSredstva).*  
      2) Ako je dostupno, rezerviše iznos *(rezervisanaSredstva \+= iznosTransakcije).*  
   2) **Poruka:**  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "RESERVE\_FUNDS",*  
        *"amount": 5000,*  
        *"currency": "EUR",*  
        *"buyer": { … },*  
        *"seller": { … }*  
      *}*  
   3) **Rollback*:*** Ako bilo koja naredna faza ne uspe, Banka A oslobađa rezervaciju *(rezervisanaSredstva \-= iznosTransakcije).*  
2) **Provera i rezervacija hartija – Banka B**  
   1) **Transakcija:**  
      1) Banka B prima zahtev od Banke A.  
      2) Proverava da li prodavac postoji i ima tražene hartije *(availableShares \>= zahtevaniBroj).*  
      3) Rezerviše hartije *(rezervisaneHartije \+= zahtevaniBroj).*  
   2) **Poruka \- success:**  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "RESERVE\_SHARES\_CONFIRM",*  
        *"sharesReserved": 100,*  
        *"asset": { … }*  
      *}*  
   3) **Poruka \- error:**  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "RESERVE\_SHARES\_FAIL",*  
        *"reason": "Insufficient shares"*  
      *}*  
   4) **Rollback*:*** Ako Banka B ne može da rezerviše hartije, Banka A oslobađa rezervisana sredstva.  
3) **Transfer sredstava – Banka A** → **Banka B**  
   1) **Transakcija**:  
      1) Banka A skida rezervisana sredstva sa računa kupca *(sredstva \-= iznosTransakcije).*  
      2) Šalje sredstva Banci B.  
   2) **Poruka**:  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "COMMIT\_FUNDS",*  
        *"amount": 5000,*  
        *"currency": "EUR",*  
        *"recipientAccount": "DE987654321"*  
      *}*  
   3) **Rollback**: Ako transfer ne uspe, Banka A vraća sredstva kupcu *(sredstva \+= iznosTransakcije).*  
4)  **Transfer vlasništva nad hartijama – Banka B** → **Banka A**  
   1) **Transakcija**:  
      1) Banka B šalje informaciju o hartijama Banci A *(npr. sertifikat vlasništva).*  
      2) Banka A ažurira da hartije pripadaju kupcu.  
   2) **Poruka (od Banke B ka Banki A)**:  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "TRANSFER\_OWNERSHIP",*  
        *"assetId": "AAPL",*  
        *"shares": 100,*  
        *"newOwner": "HR123456789"*  
      *}*  
   3) **Potvrda od Banke A**:  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "OWNERSHIP\_CONFIRM"*  
      *}*  
   4) **Rollback**:  
      1) Ako Banka A ne potvrdi vlasništvo, Banka B mora da vrati hartije prodavcu.  
      2) Banka A inicira refundaciju sredstava (ako je transfer novca već izvršen).  
5) **Double Check \- finalizacija**  
   1) **Transakcija**:  
      1) Banka A i Banka B potvrđuju da su sve faze uspešne.  
      2) Uklanjanje rezervacija *(rezervisanaSredstva \-= iznosTransakcije, rezervisaneHartije \-= brojHartija).*  
   2) **Poruka**:  
      *{*  
        *"transactionId": "123-456",*  
        *"action": "FINAL\_CONFIRM"*  
      *}* 

**Scenario Neuspeha**  
**Primer**: Ako Banka B ne potvrdi rezervaciju hartija (korak 2), Banka A odmah oslobađa rezervisana sredstva.  
**Mehanizam za Retry**:

- Ako komunikacija prekine (npr. timeout), obe banke proveravaju status transakcije preko *transactionId* i nastavljaju tamo gde su stale.  
- Primer retry poruke:

*{*  
*"transactionId": "123-456",*  
*"action": "CHECK\_STATUS"*

*}*

##### 

##### **Primer 1 \- iskorišćavanje opcionog ugovora** {#primer-1---iskorišćavanje-opcionog-ugovora}

Kupac je prethodno kupio opcionu ugovor koji mu omogućava da kupi 50 APPL akcija po ceni od $200 po akciji. Premium je platio $1150. Danas je 03.04.2025. I APPL akcije trenutno vrede **$250** po akciji. Kupac je na dobitku ako iskoristi svoju opciju:

Normalno bi platio 50 x $250 \= $12500  
Sa opcijom plaća 50 x 200 \= $10000  
Premium je platio $1150, tkd ako iskoristi opciju je u dobitku 12500 \- 10000 \- 1150 \= $1350.

1. Kupac ulazi na [Sklopljeni ugovori](#heading=h.odfmwkduot6z)  
2. Vidi da je ovaj ugovor važeći i vidi da je profit pozitivan ako bi ga iskoristio.  
3. Klik na “Iskoristi”.  
4. Transakcija se izvršava po SAGA pattern-u kao što je opisano iznad.

##### **Primer 2 \- ne iskorišćavanje opcionog ugovora** {#primer-2---ne-iskorišćavanje-opcionog-ugovora}

Kupac je prethodno kupio opcionu ugovor koji mu omogućava da kupi 50 APPL akcija po ceni od $200 po akciji. Premium je platio $1150. Danas je 03.04.2025. I APPL akcije trenutno vrede **$190** po akciji. Kupac nije na dobitku ako iskoristi svoju opciju:

Normalno bi platio 50 x $190 \= $9500  
Sa opcijom plaća 50 x 200 \= $10000  
Premium je platio $1150, tkd ako iskoristi opciju je u gubitku 11100 \- 10000 \- 1150 \= \- $1650.

1. Kupac ulazi na [Sklopljeni ugovori](#heading=h.odfmwkduot6z).  
2. Vidi da je ovaj ugovor važeći i vidi da je profit negativan ako bi ga iskoristio.  
3. Kupac ne iskorišćava opciju i u gubitku je samo za premiju (premija je manja od njegovog gubitka da je taj dan zapravo kupio akcije, $1150 \< $1650).