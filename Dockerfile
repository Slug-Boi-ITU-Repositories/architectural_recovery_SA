FROM mcr.microsoft.com/dotnet/sdk:9.0 AS dotnet

RUN git clone https://github.com/archlens/ArchLens.git

WORKDIR ArchLens

RUN dotnet publish "src/c-sharp/Archlens.csproj" -o "src/python/src/.dotnet"

FROM ubuntu:noble

RUN apt update && apt upgrade -y && apt install -y python3.12 && apt install -y python3-pip

COPY --from=dotnet . .

RUN pip install -e "src/python"

CMD ["archlens", "--help"]

