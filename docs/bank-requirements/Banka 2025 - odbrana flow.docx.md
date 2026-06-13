## 1 \- Upravljanje korisnicima

**Provera 1**

- Admin kreira zaposlenog  
- Zaposleni aktivira profil (activation email)  
- Zaposleni se uloguje na profil

**Provera 2**

- Admin deaktivira zaposlenog

## 2 \- Osnovno poslovanje banke

**Provera 1**

- Zaposleni kreira novi tekuci racun \- poslovni  
- Ne selektuje za propratnu karticu  
- Kreira novog klijenta A  
- Kreira novu firmu  
- Zaposleni kreira novi devizni racun klijenta A x2

**Provera 2**

- Zaposleni kreira novi tekuci racun \- licni  
- Selektuje da se kreira kartica za račun  
- Kreira novog klijenta B  
- Provera računa i kartice u listi kartica   
- Zaposleni kreira novi devizni racun klijenta B x2

**Provera 3 \- prenos**

- Prenos novca izmedju racuna istog klijenta \- ista valuta  
- Prenos novca izmedju racuna istog klijenta \- različita valuta  
- Provera dospeća provizije na računu banke

**Provera 4 \- plaćanje**

- Plaćanje izmedju racuna razlicitih klijenata \- ista valuta  
- Plaćanje izmedju racuna razlicith klijenata \- različita valuta  
- Provera dospeća provizije na računu banke

**Provera 5 \- krediti**

- Klijent podnosi zahtev za kredit  
- Zaposleni odobrava kredit  
- Provera da je skinut novac sa računa banke  
- Provera da je dospeo novac na račun klijenta

**Provera 6 \- kartice**

- Klijent zahteva novu karticu (poštuju se constraints za dozvoljeni broj kartica)  
- Automatski se pojavljuje kartica  
- Klijent menja limit kartice  
- Klijent blokira karticu  
- Zaposleni je deaktivira  
- Provera da je kartica deaktivirana

**Provera 7 \- izgleda menjačnice**

- Prikazuje ekvivalentnu vrednost u 2\. valuti

	

## 3 \- Trgovanje na berzi

**Provera 1 \- izgleda portala hartije od vrednosti**

- Pregled po tabovima: forex pairs, stock, futures  
- Pregled detaljanog prikaza stocka

**Provera 2 \- kupovina ForexPair-a**

- Kupovina ForexPair-a  
- Provera da se skinuo novac u *from* valuti  
- Provera da je dodat novac u *to* valuti

**Provera 3 \- kupovina stocka ili futures-a**

- Kreiranje Ordera  
  - Kllijent: kako je koji tim uradio *(prolazi automatski ili treba approval)*  
  - Aktuar: može da pređe limit pa da mu supervizor odobrava u Portal: Pregled Ordera  
- Provera hartije na Portalu: Moj portfolio

**Provera 4 \- kupovina i koriscenje opcija**

- Kupovina opcije  
- Koriscenje opcije   
- Provera dospeća stocka u “Moj portfolio”  
- Provera da se skinuo novac sa računa  
- Provera da opcija više ne može da se iskoristi/nema je

**Provera 5 \- porez**

- Korisnik vidi svoje dugovanje u poslednjih mesec dana *(tax info)*  
- Supervizor vidi svačije dugovanje za porez u portalu za porez  
- Pokreće isplatu novca  
- Provera da je novac legao na račun države  
- Provera da se novac skinuo sa računa korisnika  
- Provera da korisnik vidi plaćen porez u istorijatu *(tax info)*

## 4 \- Prosirenje trgovine hartijama od vrednosti

**Provera 1 \- OTC trade internal**

- Klijent 1 ulazi na OTC Portal i pravi ponudu za akcije klijenta 2 iz iste banke  
- Klijent 2 ulazi u Aktivne ponude i šalje kontraponudu \- provera da klijent 2 ne nudi više od available broja akcija  
- Klijent 1 ulazi u Aktivne ponude i prihvata kontraponudu  
- Klijent 1 ulazi u Sklopljene ugovore i vidi novu ponudu. Iskoristi je  
- Provera da se klijentu 1 skinuo novac  
- Provera da su klijentu 1 dodeljene akcije  
- Provera da klijent 2 više nema akcije

**Provera 2 \- OTC trade external**  
Identično kao provera 1\. Može između supervizora iz različitih banaka.

**Provera 3 \- plaćanje između banaka, različita valuta**

- Klijent iz banka A šalje klijentu iz banke B novac na račun u razl. Valuti  
- Provera da se skinuo novac pošiljaocu  
- Provera da je dospeo novac primaocu  
- Provera dospeća provizije na račun banke B (banka primalac)

## Dodatni poeni

- Korišćenje access/refresh tokena za autorizaciju  
- Funkcionalnosti u mobilnoj aplikaciji  
- Portal: Profit banke  
- …

