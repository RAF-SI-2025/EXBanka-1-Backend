# Upravljanje korisnicima {#upravljanje-korisnicima}

[Upravljanje korisnicima	0](#upravljanje-korisnicima)

[Opis	0](#opis)

[Entiteti	0](#entiteti)

[Validacija podataka prilikom unosa	2](#validacija-podataka-prilikom-unosa)

[Portal za upravljanje Zaposlenima	2](#portal-za-upravljanje-zaposlenima)

[Lista svih zaposlenih	2](#lista-svih-zaposlenih)

[Kreiranje i aktivacija naloga Zaposlenog	2](#kreiranje-i-aktivacija-naloga-zaposlenog)

[Login stranica	3](#login-stranica)

[Login \- autentifikacija korisnika	3](#login---autentifikacija-korisnika)

[Bezbednosni mehanizam za zaštitu brute-force napada	3](#bezbednosni-mehanizam-za-zaštitu-brute-force-napada)

[Autentifikacija i autorizacija korisnika	3](#autentifikacija-i-autorizacija-korisnika)

## Opis {#opis}

Prva celina obuhvata *user management* u projektu. Korisnici se dele na:

1. **Klijenti \-** Oni su "stranke" u bankama. *Imaju tekući i/ili devizni račun i osnovne funkcionalnosti koje banka pruža (uplata, isplata i prebacivanje novca, plaćanje).*  
2. **Zaposleni \-** Oni su **obični zaposleni** ili **administratori** (*kojima je dodeljena permisija administratora*). **Zaposlenima** upravljaju **administratori** preko portala (za upravljanje zaposlenih) dostupnog samo njima. *Oni će upravljati akcijama, ugovorima, osiguranjima itd.*

Može se shvatiti da ovaj projekat ima dve aplikacije:  
**Za Zaposlene:**  
1\. [Portal za “Upravljanje zaposlenima”](#portal-za-upravljanje-zaposlenima) \- *samo za administratore*

**Za Klijente:**  
Ništa u ovoj cellini.

## Entiteti {#entiteti}

**Zaposleni:**

| Podatak | Tip podatka | Primer | Učestalost promena |
| :---- | :---- | :---- | :---- |
| id | Long | 1234 | Ne menja se |
| Ime | String | Petar | Ne menja se |
| Prezime | String | Petrović | Menja se (retko) |
| Datum rodjenja | Date | (Unix vreme) | Ne menja se |
| Pol | String | M | Menja se (retko) |
| Email adresa | String | petar@primer.raf | Ne menja se |
| Broj telefona | String | \+381645555555 | Menja se (retko) |
| Adresa | String | Njegoševa 25 | Menja se (retko) |
| Username | String | petar90 | Ne menja se |
| Password | String | sifra1 \* | Menja se (retko) |
| SaltPassword | String | S4lt \*\* | Ne menja se |
| Pozicija | String | Menadžer | Menja se (retko) |
| Departman | String | Finansije | Menja se (retko) |
| Aktivan | Boolean | true | Menja se (retko) |

^Aktivan zaposleni označava da li je zaposleni trenutno u aktivnom radnom odnosu. Zaposleni imaju i listu permisija. 

**Klijent**

| Podatak | Tip podatka | Primer | Učestalost promena |
| :---- | :---- | :---- | :---- |
| id | Long | 1234 | Ne menja se |
| Ime | String | Petar | Ne menja se |
| Prezime | String | Petrović | Menja se (retko) |
| Datum rodjenja | Long | (Unix vreme) | Ne menja se |
| Pol | String | M | Menja se (retko) |
| Email adresa | String | petar@primer.raf | Ne menja se |
| Broj telefona | String | \+381645555555 | Menja se (retko) |
| Adresa | String | Njegoševa 25 | Menja se (retko) |
| Password | String | sifra1 \* | Menja se (retko) |
| SaltPassword | String | S4lt \*\* | Ne menja se |
| Povezani racuni | List/ Array | \[racun\_1, racun\_2, racun\_3, …\] | Menja se (povremeno) |

\***Password** \- hashed (and salted po zelji) password   
\*\***SaltPassword** \- dodatno polje ukoliko se koristi “*Salt and Hash*” pristup heširanja. Generiše se (random) pri pravljenju jednog korisnika i on je unikatan za svaku instancu korisnika.   
*\* Napomena: kreiranje Klijenta je u sklopu druge celine. Zaposleni kreira Klijenta prilikom [kreiranja računa](#heading=h.qowc3jc1va80)* 

### Validacija podataka prilikom unosa {#validacija-podataka-prilikom-unosa}

Email mora biti jedinstven i mora biti validnog formata ([banka@primer.rs](mailto:banka@primer.rs))  
Broj telefona može sadržati samo cifre i znak “+” na početku  
Datum rodjenja ne sme biti u budoćnosti I mora odgovarati nekom formatu

## Portal za upravljanje Zaposlenima {#portal-za-upravljanje-zaposlenima}

Mogu da pristupe samo zaposleni koji su administratori. Imaju pregled svih zaposlenih (admin i običnog zaposlenog), ali mogu da edituju samo običnog zaposlenog. 

### Lista svih zaposlenih {#lista-svih-zaposlenih}

**Prikaz:** Administrator vidi sve zaposlene, prikazani su *ime i prezime, email, pozicija, broj telefona i da li je aktivan ili ne.*   
**Filtriranje:** Moguće je filtriranje po email-u, imenu i prezimenu, kao i poziciji.  
**Klikom na običnog zaposlenog** otvara se novi prozor gde administrator može da izmeni sve informacija osim ID-a i passworda. **Napomena**: može da deaktivira radnika i upravlja permisijama (može i da dodeli admin permisiju).  
*\*Ukoliko je zaposleni trenutno ulogovan u sistem u trenutku deaktivacije sve aktivne sesije se automatski prekidaju I korisnik se izloguje iz sistema*

**\* Permisije:** Svaki zaposleni ima set permisija odnosno šta mu je dozvoljeno da radi na sistemu (npr. trguje akcijama, samo gleda akcije, sklapa ugovore i nova osiguranja...).

### Kreiranje i aktivacija naloga Zaposlenog {#kreiranje-i-aktivacija-naloga-zaposlenog}

Samo **administrator** može da kreira nalog zaposlenog. 

Administrator unosi sva polja osim password-a. Po default-u se podrazumeva da je korisnik aktivan, ali moguće je napraviti i korisnika koji nije aktivan. Nakon što **administrator kreira zaposlenog**, potrebno je omogućiti zaposlenom da može da **aktivira nalog** i unese svoju lozinku. To na primer može biti realizovano tako što se korisniku pošalje mejl sa linkom koji otvara formu za unos lozinke (2 polja radi potvrde lozinke). Nakon toga dobija confirmation mail.

**\* Unikatnost naloga:** u bazi ne smeju biti 2 naloga sa istim emailom (*email je unique*)  
**\* Password constraints:** najmanje 8, a najviše 32 karaktera, sa najmanje 2 broja, 1 velikim i 1 malim slovom.

## Login stranica {#login-stranica}

Na login stranici postoje 1 funkcionalnosti \- login.

### Login \- autentifikacija korisnika {#login---autentifikacija-korisnika}

Korisnik se loguje u sistem unosom email-a i password-a. Korisnik takođe ima opciju da resetuje svoju lozinku ako ju je zaboravio \- ovo se takođe npr. može uraditi preko email-a sa linkom ka stranici za resetovanje lozinke. Dodatno, zbog sigurnosti, ukoliko korisnik zatvori svoj web pretraživač potrebno je tražiti da se korisnik ponovno uloguje. 

### Bezbednosni mehanizam za zaštitu brute-force napada {#bezbednosni-mehanizam-za-zaštitu-brute-force-napada}

Sistem prati broj uzastopnih neuspelih pokušaja prijava  
Dodatna polja u entitetu: failedLoginAttempts (int), accountLockedUntil (DateTime)  
Pravila: Nakon 5 neuspešnih pokušaja, nalog se zaključava na 10 minuta, tokom tog perioda nije moguće pokušati logovanje, nakon isteka vremena, korisnik može ponovo pokušati  
Bezbednosne mere: Sistem šalje email korisniku nakon što nalog bude zaključan (tj. nakon 5 uzastopnih neuspelih pokušaja). Email sadrži obaveštenje o zaključavanju naloga i opciju za reset lozinke koji otključava nalog i resetuje broj neuspešnih pokušaja.  
Reset lozinke: otključava nalog i resetuje broj neuspešnih pokušaja

## Autentifikacija i autorizacija korisnika {#autentifikacija-i-autorizacija-korisnika}

Autentifikacija korisnika je gore opisana u okviru logina na *Login stranici.* 

**Autorizacija mora** **biti implementirana preko access/refresh tokena.** Autorizacija korisnika se odnosi na to da za izvršenje većine zahteva/operacija, potrebno je proveriti da li korisnik ima permisiju da izvrši tu operaciju. Ukoliko nema, vraća se odgovarajuća greška.   
*\* U opštem slučaju, korisnici ne bi trebalo da su svesni o postojanju operacija za koje nemaju dozvolu.*